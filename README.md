# Loraserver
Version adapted by Jori, purpose: heterogeneous interconnection of IoT networks


## Docker-compose
'''bash
sudo apt-get -y install python-pip
sudo pip install docker-compose
'''
source: https://gist.github.com/oysteinjakobsen/e59cdd38a688ee8a418a

### Building on raspberry pi:
It is possible to install docker and docker-compose on raspberry pi.
Problem: No official ARM golang images available for docker
source: http://blog.alexellis.io/getting-started-with-docker-on-raspberry-pi/

## Building from source

The easiest way to get started is by using the provided 
[docker-compose](https://docs.docker.com/compose/) environment. To start a bash
shell within the docker-compose environment, execute the following command from
the root of this project:

```bash
docker-compose run --rm loraserver bash
```

A few example commands that you can run:

```bash
# run the tests
make test

# compile
make build

# cross-compile for Linux ARM
GOOS=linux GOARCH=arm make build

# cross-compile for Windows AMD64
GOOS=windows BINEXT=.exe GOARCH=amd64 make build

# build the .tar.gz file
make package

# build the .tar.gz file for Linux ARM
GOOS=linux GOARCH=arm make package

# build the .tar.gz file for Windows AMD64
GOOS=windows BINEXT=.exe GOARCH=amd64 make package
```

Alternatively, you can run the same commands from any working
[Go](https://golang.org/) environment. As all requirements are vendored,
there is no need to `go get` these. Make sure you have Go 1.7.x installed
and that you clone this repository to
`$GOPATH/src/github.com/joriwind/loraserver`.