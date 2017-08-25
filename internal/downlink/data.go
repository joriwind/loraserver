package downlink

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/pkg/errors"

	"github.com/joriwind/loraserver/api/as"
	"github.com/joriwind/loraserver/api/gw"
	"github.com/joriwind/loraserver/api/nc"
	"github.com/joriwind/loraserver/internal/common"
	"github.com/joriwind/loraserver/internal/maccommand"
	"github.com/joriwind/loraserver/internal/models"
	"github.com/joriwind/loraserver/internal/session"
	"github.com/brocaar/lorawan"
)

// DataDownFrameContext describes the context for a downlink frame.
type DataDownFrameContext struct {
	// ACK defines if ACK must be set to true (e.g. the frame acknowledges
	// an uplink frame).
	ACK bool

	// FPort to use for transmission. This must be set to a value != 0 in case
	// Data is not empty.
	FPort uint8

	// MACCommands contains the mac-commands to send (if any). Make sure the
	// total size fits within the FRMPayload or FPort (depending on if
	// EncryptMACCommands is set to true).
	MACCommands []lorawan.MACCommand

	// EncryptMACCommands defines if the mac-commands (if any) must be
	// encrypted and put in the FRMPayload. Note that it is not possible to
	// send encrypted mac-commands when FPort is set to a value != 0.
	EncryptMACCommands bool

	// Confirmed defines if the frame must be send as confirmed-data.
	Confirmed bool

	// MoreData defines if there is more data pending.
	MoreData bool

	// Data contains the bytes to send. Note that this requires FPort to be a
	// value other than 0.
	Data []byte
}

// Validate validates the correctness of DataDownFrameContext.
func (ctx DataDownFrameContext) Validate() error {
	if ctx.FPort == 0 && len(ctx.Data) > 0 {
		return ErrFPortMustNotBeZero
	}

	if ctx.FPort > 0 && ctx.EncryptMACCommands {
		return ErrFPortMustBeZero
	}

	return nil
}

// SendDataDown sends the given data to the gateway for transmission.
func SendDataDown(ctx common.Context, ns *session.NodeSession, txInfo gw.TXInfo, dataDown DataDownFrameContext) error {
	if err := dataDown.Validate(); err != nil {
		return errors.Wrap(err, "validation error")
	}

	phy := lorawan.PHYPayload{
		MHDR: lorawan.MHDR{
			MType: lorawan.UnconfirmedDataDown,
			Major: lorawan.LoRaWANR1,
		},
	}
	if dataDown.Confirmed {
		phy.MHDR.MType = lorawan.ConfirmedDataDown
	}

	macPL := &lorawan.MACPayload{
		FHDR: lorawan.FHDR{
			DevAddr: ns.DevAddr,
			FCtrl: lorawan.FCtrl{
				ADR:      ns.ADRInterval != 0,
				ACK:      dataDown.ACK,
				FPending: dataDown.MoreData,
			},
			FCnt: ns.FCntDown,
		},
	}
	phy.MACPayload = macPL

	if len(dataDown.MACCommands) > 0 {
		if dataDown.EncryptMACCommands {
			var frmPayload []lorawan.Payload
			for i := range dataDown.MACCommands {
				frmPayload = append(frmPayload, &dataDown.MACCommands[i])
			}
			macPL.FPort = &dataDown.FPort
			macPL.FRMPayload = frmPayload

			// encrypt the FRMPayload with the NwkSKey
			if err := phy.EncryptFRMPayload(ns.NwkSKey); err != nil {
				return errors.Wrap(err, "encrypt FRMPayload error")
			}
		} else {
			macPL.FHDR.FOpts = dataDown.MACCommands
		}
	}

	if dataDown.FPort > 0 {
		macPL.FPort = &dataDown.FPort
		macPL.FRMPayload = []lorawan.Payload{
			&lorawan.DataPayload{Bytes: dataDown.Data},
		}
	}

	if err := phy.SetMIC(ns.NwkSKey); err != nil {
		return errors.Wrap(err, "set MIC error")
	}

	// send the packet to the gateway
	if err := ctx.Gateway.SendTXPacket(gw.TXPacket{
		TXInfo:     txInfo,
		PHYPayload: phy,
	}); err != nil {
		return errors.Wrap(err, "send tx packet to gateway error")
	}

	// increment the FCntDown when Confirmed = false
	if !dataDown.Confirmed {
		ns.FCntDown++
		if err := session.SaveNodeSession(ctx.RedisPool, *ns); err != nil {
			return errors.Wrap(err, "save node-session error")
		}
	}

	return nil
}

// HandlePushDataDown handles requests to push data to a given node.
func HandlePushDataDown(ctx common.Context, ns session.NodeSession, confirmed bool, fPort uint8, data []byte) error {
	if len(ns.LastRXInfoSet) == 0 {
		return ErrNoLastRXInfoSet
	}

	dr := int(ns.RX2DR)
	if dr > len(common.Band.DataRates)-1 {
		return errors.Wrapf(ErrInvalidDataRate, "dr: %d (max dr: %d)", dr, len(common.Band.DataRates)-1)
	}

	remainingPayloadSize := common.Band.MaxPayloadSize[dr].N
	if len(data) > remainingPayloadSize {
		return errors.Wrapf(ErrMaxPayloadSizeExceeded, "(max: %d)", remainingPayloadSize)
	}
	remainingPayloadSize = remainingPayloadSize - len(data)

	macQueueItems, _, _, err := getAndFilterMACQueueItems(ctx, ns, false, remainingPayloadSize)
	if err != nil {
		return errors.Wrap(err, "get mac-commands error")
	}
	macCommands := macQueueItemsToMACCommands(ctx, ns, macQueueItems)

	txInfo := gw.TXInfo{
		MAC:         ns.LastRXInfoSet[0].MAC,
		Immediately: true,
		Frequency:   int(common.Band.RX2Frequency),
		Power:       common.Band.DefaultTXPower,
		DataRate:    common.Band.DataRates[dr],
		CodeRate:    "4/5",
	}

	ddCTX := DataDownFrameContext{
		FPort:       fPort,
		Data:        data,
		Confirmed:   confirmed,
		MACCommands: macCommands,
	}

	if err := SendDataDown(ctx, &ns, txInfo, ddCTX); err != nil {
		return errors.Wrap(err, "send data down error")
	}

	// remove the transmitted mac commands from the queue
	for _, qi := range macQueueItems {
		if err = maccommand.DeleteQueueItem(ctx.RedisPool, ns.DevEUI, qi); err != nil {
			return errors.Wrap(err, "delete mac-command queue item error")
		}
	}

	return nil
}

// SendUplinkResponse sends the data-down response to an uplink packet.
// A downlink response happens when: there is data in the downlink queue,
// there are MAC commmands to send and / or when the uplink packet was of
// type ConfirmedDataUp, so an ACK response is needed.
func SendUplinkResponse(ctx common.Context, ns session.NodeSession, rxPacket models.RXPacket) error {
	macPL, ok := rxPacket.PHYPayload.MACPayload.(*lorawan.MACPayload)
	if !ok {
		return fmt.Errorf("expected *lorawan.MACPayload, got: %T", rxPacket.PHYPayload.MACPayload)
	}

	// get data down tx properties
	txInfo, dr, err := getDataDownTXInfoAndDR(ctx, ns, rxPacket.RXInfoSet[0])
	if err != nil {
		return fmt.Errorf("get data down txinfo error: %s", err)
	}

	allowEncryptedMACCommands := true
	remainingPayloadSize := common.Band.MaxPayloadSize[dr].N

	// get data down from application-server (if it has anything in its queue)
	txPayload := getDataDownFromApplication(ctx, ns, dr)

	// get mac-commands to fill the remaining payload bytes
	if txPayload != nil {
		remainingPayloadSize = remainingPayloadSize - len(txPayload.Data)
		allowEncryptedMACCommands = false
	}

	// read mac-commands queue items
	macQueueItems, encryptMACCommands, pendingMACCommands, err := getAndFilterMACQueueItems(ctx, ns, allowEncryptedMACCommands, remainingPayloadSize)
	if err != nil {
		return fmt.Errorf("get mac-commands error: %s", err)
	}
	macCommands := macQueueItemsToMACCommands(ctx, ns, macQueueItems)

	ddCTX := DataDownFrameContext{
		ACK:         rxPacket.PHYPayload.MHDR.MType == lorawan.ConfirmedDataUp,
		MACCommands: macCommands,
	}

	if txPayload != nil {
		ddCTX.Confirmed = txPayload.Confirmed
		ddCTX.MoreData = txPayload.MoreData
		ddCTX.FPort = uint8(txPayload.FPort)
		ddCTX.Data = txPayload.Data
	}

	if pendingMACCommands {
		ddCTX.MoreData = true
	}

	if allowEncryptedMACCommands && encryptMACCommands {
		ddCTX.EncryptMACCommands = true
	}

	// Uplink was unconfirmed and no downlink data in queue and no mac commands to send.
	// Note: in case of a ADRACKReq we still need to respond.
	if txPayload == nil && !ddCTX.ACK && len(ddCTX.MACCommands) == 0 && !macPL.FHDR.FCtrl.ADRACKReq {
		return nil
	}

	// send the data to the node
	if err := SendDataDown(ctx, &ns, txInfo, ddCTX); err != nil {
		return fmt.Errorf("send data down error: %s", err)
	}

	// remove the transmitted mac commands from the queue
	for _, qi := range macQueueItems {
		if err = maccommand.DeleteQueueItem(ctx.RedisPool, ns.DevEUI, qi); err != nil {
			return fmt.Errorf("delete mac-command queue item from queue error: %s", err)
		}
	}

	return nil
}

func getDataDownTXInfoAndDR(ctx common.Context, ns session.NodeSession, rxInfo gw.RXInfo) (gw.TXInfo, int, error) {
	var dr int
	txInfo := gw.TXInfo{
		MAC:      rxInfo.MAC,
		CodeRate: rxInfo.CodeRate,
		Power:    common.Band.DefaultTXPower,
	}

	if ns.RXWindow == session.RX1 {
		uplinkDR, err := common.Band.GetDataRate(rxInfo.DataRate)
		if err != nil {
			return txInfo, dr, err
		}

		// get rx1 dr
		dr, err = common.Band.GetRX1DataRate(uplinkDR, int(ns.RX1DROffset))
		if err != nil {
			return txInfo, dr, err
		}
		txInfo.DataRate = common.Band.DataRates[dr]

		// get rx1 frequency
		txInfo.Frequency, err = common.Band.GetRX1Frequency(rxInfo.Frequency)
		if err != nil {
			return txInfo, dr, err
		}

		// get timestamp
		txInfo.Timestamp = rxInfo.Timestamp + uint32(common.Band.ReceiveDelay1/time.Microsecond)
		if ns.RXDelay > 0 {
			txInfo.Timestamp = rxInfo.Timestamp + uint32(time.Duration(ns.RXDelay)*time.Second/time.Microsecond)
		}
	} else if ns.RXWindow == session.RX2 {
		// rx2 dr
		dr = int(ns.RX2DR)
		if dr > len(common.Band.DataRates)-1 {
			return txInfo, 0, fmt.Errorf("invalid rx2 dr: %d (max dr: %d)", dr, len(common.Band.DataRates)-1)
		}
		txInfo.DataRate = common.Band.DataRates[dr]

		// rx2 frequency
		txInfo.Frequency = common.Band.RX2Frequency

		// rx2 timestamp (rx1 + 1 sec)
		txInfo.Timestamp = rxInfo.Timestamp + uint32(common.Band.ReceiveDelay1/time.Microsecond)
		if ns.RXDelay > 0 {
			txInfo.Timestamp = rxInfo.Timestamp + uint32(time.Duration(ns.RXDelay)*time.Second/time.Microsecond)
		}
		txInfo.Timestamp = txInfo.Timestamp + uint32(time.Second/time.Microsecond)
	} else {
		return txInfo, dr, fmt.Errorf("unknown RXWindow option %d", ns.RXWindow)
	}

	return txInfo, dr, nil
}

// getDataDownFromApplication gets the downlink data from the application
// (if any). On error the error is logged.
func getDataDownFromApplication(ctx common.Context, ns session.NodeSession, dr int) *as.GetDataDownResponse {
	resp, err := ctx.Application.GetDataDown(context.Background(), &as.GetDataDownRequest{
		AppEUI:         ns.AppEUI[:],
		DevEUI:         ns.DevEUI[:],
		MaxPayloadSize: uint32(common.Band.MaxPayloadSize[dr].N),
		FCnt:           ns.FCntDown,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"dev_eui": ns.DevEUI,
			"fcnt":    ns.FCntDown,
		}).Errorf("get data down from application error: %s", err)
		return nil
	}

	if resp == nil || resp.FPort == 0 {
		return nil
	}

	if len(resp.Data) > common.Band.MaxPayloadSize[dr].N {
		log.WithFields(log.Fields{
			"dev_eui":          ns.DevEUI,
			"size":             len(resp.Data),
			"max_payload_size": common.Band.MaxPayloadSize[dr].N,
			"dr":               dr,
		}).Warning("data down from application exceeds max payload size")
		return nil
	}

	log.WithFields(log.Fields{
		"dev_eui":     ns.DevEUI,
		"fcnt":        ns.FCntDown,
		"data_base64": base64.StdEncoding.EncodeToString(resp.Data),
		"confirmed":   resp.Confirmed,
		"more_data":   resp.MoreData,
	}).Info("received data down from application")

	return resp
}

// getAndFilterMACQueueItems returns the mac-commands to send, based on the constraints:
// - allowEncrypted: when set to true, the FRMPayload may be used for
//   (encrypted) mac-commands, else only FOpt mac-commands will be returned
// - remainingPayloadSize: the number of bytes that are left for mac-commands
// It returns:
// - a slice of mac-command queue items
// - if the mac-commands must be put into FRMPayload
// - if there are remaining mac-commands in the queue
func getAndFilterMACQueueItems(ctx common.Context, ns session.NodeSession, allowEncrypted bool, remainingPayloadSize int) ([]maccommand.QueueItem, bool, bool, error) {
	var encrypted bool

	// read the mac payload queue
	queueItems, err := maccommand.ReadQueue(ctx.RedisPool, ns.DevEUI)
	if err != nil {
		return nil, false, false, fmt.Errorf("read mac-payload tx queue error: %s", err)
	}
	macCommandQueueSize := len(queueItems)

	// nothing to do
	if len(queueItems) == 0 {
		return nil, false, false, nil
	}

	// encrypted mac-commands are allowed and the first mac-command in the
	// queue is marked to be encrypted
	if allowEncrypted && queueItems[0].FRMPayload {
		encrypted = true
		queueItems = maccommand.FilterItems(queueItems, true, remainingPayloadSize)
	} else {
		maxFOptsLen := remainingPayloadSize
		// the LoRaWAN specs define 15 to be the max FOpts size
		if maxFOptsLen > 15 {
			maxFOptsLen = 15
		}
		queueItems = maccommand.FilterItems(queueItems, false, maxFOptsLen)
	}

	return queueItems, encrypted, len(queueItems) != macCommandQueueSize, nil
}

// macQueueItemsToMACCommands converts a slice of queue items into lorawan
// mac-command format. When it can't unmarshal the queue item into
// mac-command, a warning is logged and the network-controller backend
// is notified.
func macQueueItemsToMACCommands(ctx common.Context, ns session.NodeSession, items []maccommand.QueueItem) []lorawan.MACCommand {
	var out []lorawan.MACCommand

	for _, qi := range items {
		var mac lorawan.MACCommand
		if err := mac.UnmarshalBinary(false, qi.Data); err != nil {
			// in case the mac commands can't be unmarshaled, the payload
			// is ignored and an error sent to the network-controller
			errStr := fmt.Sprintf("unmarshal mac command error: %s", err)
			log.WithFields(log.Fields{
				"dev_eui":     ns.DevEUI,
				"command_hex": hex.EncodeToString(qi.Data),
			}).Warning(errStr)
			_, err = ctx.Controller.HandleError(context.Background(), &nc.HandleErrorRequest{
				AppEUI: ns.AppEUI[:],
				DevEUI: ns.DevEUI[:],
				Error:  errStr + fmt.Sprintf(" (command: %X)", qi.Data),
			})
			if err != nil {
				log.Errorf("call controller handle error method error: %s", err)
			}
			continue
		}
		out = append(out, mac)
	}

	return out
}
