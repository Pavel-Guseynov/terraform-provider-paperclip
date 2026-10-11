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

func run(ctx context.Context, opts providerserver.ServeOpts) error {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.CleanupAllUnconfirmedKeys(cleanupCtx)
	}()

	return providerserver.Serve(ctx, provider.New(version), opts)
}

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

	if err := run(ctx, opts); err != nil {
		log.Println(err.Error())
		os.Exit(1)
	}
}
