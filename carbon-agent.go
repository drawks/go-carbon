package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"runtime"
	"strconv"
	"syscall"

	"github.com/lomik/go-carbon/carbon"
	log "github.com/lomik/go-carbon/logging"
	"github.com/sevlyar/go-daemon"
)

import _ "net/http/pprof"

// Version of go-carbon
const Version = "0.8.0"

func httpServe(addr string) (func(), error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}

	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return nil, err
	}

	go http.Serve(listener, nil)
	return func() { listener.Close() }, nil
}

func init() {
	// signal watcher
	signalChan := make(chan os.Signal, 16)
	signal.Notify(signalChan, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-signalChan:
				std := log.StandardLogger()
				err := std.Reopen()
				log.Infof("HUP received, reopen log %#v", std.Filename())
				if err != nil {
					log.Errorf("Reopen log %#v failed: %s", std.Filename(), err.Error())
				}
			}
		}
	}()
}

func main() {
	var err error

	/* CONFIG start */

	configFile := flag.String("config", "", "Filename of config")
	printDefaultConfig := flag.Bool("config-print-default", false, "Print default config")
	checkConfig := flag.Bool("check-config", false, "Check config and exit")

	printVersion := flag.Bool("version", false, "Print version")

	isDaemon := flag.Bool("daemon", false, "Run in background")
	pidfile := flag.String("pidfile", "", "Pidfile path (only for daemon)")

	flag.Parse()

	if *printVersion {
		fmt.Print(Version)
		return
	}

	if *printDefaultConfig {
		if err = carbon.PrintConfig(carbon.NewConfig()); err != nil {
			log.Fatal(err)
		}
		return
	}

	app := carbon.New(*configFile)

	if err = app.ParseConfig(); err != nil {
		log.Fatal(err)
	}

	cfg := app.Config

	var runAsUser *user.User
	if cfg.Common.User != "" {
		runAsUser, err = user.Lookup(cfg.Common.User)
		if err != nil {
			log.Fatal(err)
		}
	}

	if err := log.SetLevel(cfg.Common.LogLevel); err != nil {
		log.Fatal(err)
	}

	// config parsed successfully. Exit in check-only mode
	if *checkConfig {
		return
	}

	if err := log.PrepareFile(cfg.Common.Logfile, runAsUser); err != nil {
		log.Fatal(err)
	}

	if err := log.SetFile(cfg.Common.Logfile); err != nil {
		log.Fatal(err)
	}

	if *isDaemon {
		runtime.LockOSThread()

		context := new(daemon.Context)
		if *pidfile != "" {
			context.PidFileName = *pidfile
			context.PidFilePerm = 0644
		}

		if runAsUser != nil {
			uid, err := strconv.ParseInt(runAsUser.Uid, 10, 0)
			if err != nil {
				log.Fatal(err)
			}

			gid, err := strconv.ParseInt(runAsUser.Gid, 10, 0)
			if err != nil {
				log.Fatal(err)
			}

			context.Credential = &syscall.Credential{
				Uid: uint32(uid),
				Gid: uint32(gid),
			}
		}

		child, _ := context.Reborn()

		if child != nil {
			return
		}
		defer context.Release()

		runtime.UnlockOSThread()
	}

	runtime.GOMAXPROCS(cfg.Common.MaxCPU)

	/* CONFIG end */

	// pprof
	httpStop := func() {}
	if cfg.Pprof.Enabled {
		httpStop, err = httpServe(cfg.Pprof.Listen)
		if err != nil {
			log.Fatal(err)
		}
	}

	if err = app.Start(); err != nil {
		log.Fatal(err)
	} else {
		log.Info("started")
	}

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGUSR2)
		<-c
		httpStop()
		app.GraceStop()
	}()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGHUP)
		for {
			<-c
			log.Info("HUP received. Reload config")
			if err := app.ReloadConfig(); err != nil {
				log.Errorf("Config reload failed: %s", err.Error())
			} else {
				log.Info("Config successfully reloaded")
			}
		}
	}()

	app.Loop()

	log.Info("stopped")
}
