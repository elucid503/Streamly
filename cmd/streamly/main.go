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
	"streamly/internal/workers"
)

func main() {

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := workers.NewStore("workers.json")
	p := pool.New(store)

	if err := p.LoadWorkers(ctx); err != nil {

		log.Fatalf("workers load failed: %v", err)

	}

	log.Printf("[workers] streaming workers ready: %d", p.Size())

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
