package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"streamly/internal/bot"
	"streamly/internal/config"
	"streamly/internal/db"
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

	database, err := db.Connect(ctx, config.App.MongoURI)

	if err != nil {

		log.Fatalf("mongo connect failed: %v", err)

	}

	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = database.Close(shutdown)
	}()

	resolver := media.NewResolver()
	app, err := bot.New(resolver, p, database)

	if err != nil {

		log.Fatalf("bot init failed: %v", err)

	}

	if err := app.Start(); err != nil {

		log.Fatalf("bot start failed: %v", err)

	}

	<-ctx.Done()

	_ = app.Session.Close()

}
