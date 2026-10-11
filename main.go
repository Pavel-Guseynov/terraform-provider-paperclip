package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var (
	version string = "0.1.0"
)

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "git.guseynov.org/guseynov/paperclip",
		Debug:   debug,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client.CleanupAllUnconfirmedKeys(cleanupCtx)
	}()

	err := providerserver.Serve(ctx, provider.New(version), opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}
