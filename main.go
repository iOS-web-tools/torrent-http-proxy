package main

import (
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func main() {
	// log.SetFormatter(joonix.NewFormatter())
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	app := cli.NewApp()
	app.Name = "torrent-http-proxy"
	app.Usage = "Proxies all the things"
	app.Version = "0.0.1"
	configure(app)
	err := app.Run(os.Args)
	if err != nil {
		log.WithError(err).Fatal("Failed to serve application")
	}
}
