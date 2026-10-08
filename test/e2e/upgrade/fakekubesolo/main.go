// fakekubesolo is a stand-in for a broken KubeSolo release, for exercising the
// upgrade's failure handling end to end. It is a real executable for the host,
// and reports the version it was built with, so it passes every check made
// before the switch-over; what it does after depends on how it was built:
//
//	go build -ldflags "-X main.Version=v9.0.0-crash -X main.Mode=crash"
//
// Modes:
//
//	crash  exits with an error as soon as it is started as KubeSolo
//	hang   starts and never becomes healthy
//	baddb  fails the datastore dry-run
//
// It refuses --upgrade-executor, like a release from before the executor, so
// the upgrade has to be driven by the installed binary.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var (
	Version = "v0.0.0-fake"
	Mode    = "crash"
)

func main() {
	for _, arg := range os.Args[1:] {
		switch {
		case arg == "--version" || arg == "-v":
			fmt.Printf(`{"level":"info","version":"%s","message":"kubesolo version"}`+"\n", Version)
			return
		case strings.HasPrefix(arg, "--upgrade-executor"):
			fmt.Fprintln(os.Stderr, "kubesolo: error: unknown long flag '--upgrade-executor', try --help")
			os.Exit(1)
		case strings.HasPrefix(arg, "--upgrade-check-datastore"):
			if Mode == "baddb" {
				fmt.Fprintln(os.Stderr, "kine could not open the datastore: simulated migration failure")
				os.Exit(1)
			}
			fmt.Println("datastore ok: simulated")
			return
		}
	}

	switch Mode {
	case "hang":
		fmt.Println("fake KubeSolo started and will never be healthy")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
	default:
		fmt.Fprintln(os.Stderr, "fake KubeSolo crashed on start")
		os.Exit(1)
	}
}
