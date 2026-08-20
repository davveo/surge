package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	"github.com/davveo/surge/pkg/route"
	"github.com/davveo/surge/pkg/wsframe"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

type Conn struct {
	id       string
	uid      string
	deviceID string
	binary   bool
	ws       *websocket.Conn
	send     chan *imv1.Envelope
	hub      *Hub
}

type Hub struct {
	mu     sync.Mutex
	byConn map[string]*Conn
	byUID  map[string]*Conn
	rdb    *redis.Client
	gwID   string
}

func newHub(rdb *redis.Client, gwID string) *Hub {
	return &Hub{
		byConn: map[string]*Conn{},
		byUID:  map[string]*Conn{},
		rdb:    rdb,
		gwID:   gwID,
	}
}

func (h *Hub) bind(c *Conn) *Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	var kicked *Conn
	if old := h.byUID[c.uid]; old != nil && old.id != c.id {
		kicked = old
		delete(h.byConn, old.id)
	}
	h.byConn[c.id] = c
	h.byUID[c.uid] = c
	return kicked
}

func (h *Hub) unbind(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.byConn[c.id]; ok && cur == c {
		delete(h.byConn, c.id)
	}
	if cur, ok := h.byUID[c.uid]; ok && cur == c {
		delete(h.byUID, c.uid)
	}
}

func (h *Hub) push(gp *imv1.GatewayPush) {
	if gp == nil || gp.Push == nil {
		return
	}
	h.mu.Lock()
	c := h.byUID[gp.Uid]
	h.mu.Unlock()
	if c == nil {
		return
	}
	if gp.ConnId != "" && gp.ConnId != c.id {
		return
	}
	c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Push{Push: gp.Push}})
}

func (c *Conn) enqueue(env *imv1.Envelope) {
	select {
	case c.send <- env:
	default:
		log.Printf("conn %s send queue full, drop %T", c.id, env.Body)
	}
}

func (h *Hub) registerRoute(ctx context.Context, c *Conn) error {
	b, err := route.Encode(route.Record{GatewayID: h.gwID, ConnID: c.id})
	if err != nil {
		return err
	}
	return h.rdb.Set(ctx, route.Key(c.uid), b, route.TTL).Err()
}

func (h *Hub) refreshRoute(ctx context.Context, c *Conn) {
	if err := h.rdb.Expire(ctx, route.Key(c.uid), route.TTL).Err(); err != nil {
		log.Printf("refresh route %s: %v", c.uid, err)
	}
}

func (h *Hub) clearRoute(ctx context.Context, c *Conn) {
	key := route.Key(c.uid)
	raw, err := h.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return
	}
	rec, err := route.Decode(raw)
	if err != nil || rec == nil {
		return
	}
	if rec.ConnID == c.id && rec.GatewayID == h.gwID {
		_ = h.rdb.Del(ctx, key).Err()
	}
}

func newConn(ws *websocket.Conn, hub *Hub) *Conn {
	return &Conn{
		id:     uuid.NewString(),
		binary: true,
		ws:     ws,
		send:   make(chan *imv1.Envelope, 64),
		hub:    hub,
	}
}

func (c *Conn) writeLoop() {
	defer c.ws.Close()
	for env := range c.send {
		payload, err := wsframe.Encode(c.binary, env)
		if err != nil {
			log.Printf("encode: %v", err)
			return
		}
		msgType := websocket.BinaryMessage
		if !c.binary {
			msgType = websocket.TextMessage
		}
		_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.ws.WriteMessage(msgType, payload); err != nil {
			return
		}
	}
}

func (h *Hub) listenPushes(ctx context.Context) {
	sub := h.rdb.Subscribe(ctx, route.Channel(h.gwID))
	ch := sub.Channel()
	log.Printf("gateway %s subscribed %s", h.gwID, route.Channel(h.gwID))
	for {
		select {
		case <-ctx.Done():
			_ = sub.Close()
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			gp := &imv1.GatewayPush{}
			if err := proto.Unmarshal([]byte(msg.Payload), gp); err != nil {
				// allow JSON for debugging
				if err2 := json.Unmarshal([]byte(msg.Payload), gp); err2 != nil {
					log.Printf("push decode: %v", err)
					continue
				}
			}
			h.push(gp)
		}
	}
}
