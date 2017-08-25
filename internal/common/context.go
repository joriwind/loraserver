package common

import (
	"github.com/garyburd/redigo/redis"
	"github.com/jmoiron/sqlx"

	"github.com/joriwind/loraserver/api/as"
	"github.com/joriwind/loraserver/api/nc"
	"github.com/joriwind/loraserver/internal/backend"
	"github.com/brocaar/lorawan"
)

// Context holds the context of a loraserver instance
// (backends, db connections etc..)
type Context struct {
	RedisPool   *redis.Pool
	DB          *sqlx.DB
	Gateway     backend.Gateway
	NetID       lorawan.NetID
	Application as.ApplicationServerClient
	Controller  nc.NetworkControllerClient
}
