package main

import (
	"context"
	"database/sql"
	_ "embed"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

//go:embed schema.sql
var schemaSQL string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := loadConfig()

	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("mysql open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(32)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := pingWait(db, 30*time.Second); err != nil {
		log.Fatalf("mysql ping: %v", err)
	}
	if err := migrate(db, schemaSQL); err != nil {
		log.Fatalf("migrate: %v", err)
	}

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

	store := newMySQLStore(db, nil)
	srvImpl := newServer(store, newRedisRouter(rdb))
	srvImpl.notify = newMailer(store)
	srv := grpc.NewServer()
	imv1.RegisterIMCoreServer(srv, srvImpl)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("im-core gRPC %s", cfg.GRPCAddr)

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		log.Printf("im-core shutting down")
		srv.GracefulStop()
	}()

	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func pingWait(db *sql.DB, d time.Duration) error {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		last = db.Ping()
		if last == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return last
}
