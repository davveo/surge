package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := loadConfig()

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPass,
		DB:       cfg.RedisDB,
	})
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis ping: %v", err)
	}
	cancel()

	conn, err := grpc.Dial(cfg.IMCoreAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("im-core dial: %v", err)
	}
	defer conn.Close()
	core := imv1.NewIMCoreClient(conn)

	hub := newHub(rdb, cfg.GatewayID)
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	go hub.listenPushes(subCtx)

	api := &httpAPI{
		secret: cfg.JWTSecret,
		core:   core,
		webDir: cfg.WebDir,
		ws: &wsServer{
			hub:    hub,
			core:   core,
			secret: cfg.JWTSecret,
			idle:   cfg.IdleTimeout,
		},
	}

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: api.routes()}
	go func() {
		log.Printf("gateway %s http %s → im-core %s", cfg.GatewayID, cfg.HTTPAddr, cfg.IMCoreAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	subCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
