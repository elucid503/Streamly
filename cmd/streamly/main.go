package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"streamly/internal/bot"
	"streamly/internal/config"
	"streamly/internal/media"
	"streamly/internal/pool"
)

func main() {

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	p := pool.New()

	if err := p.Login(ctx, config.App.UserTokens); err != nil {

		log.Fatalf("pool login failed: %v", err)

	}

	if p.Size() == 0 {

		log.Fatal("no streaming accounts logged in; set USER_TOKENS to one or more valid user tokens")

	}

	log.Printf("[pool] streaming accounts ready: %d/%d", p.Size(), len(config.App.UserTokens))

	resolver := media.NewResolver()
	app, err := bot.New(resolver, p)

	if err != nil {

		log.Fatalf("bot init failed: %v", err)

	}

	if err := app.Start(); err != nil {

		log.Fatalf("bot start failed: %v", err)

	}

	<-ctx.Done()

	_ = app.Session.Close()

}
