package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/davveo/surge/pkg/route"
	"github.com/davveo/surge/pkg/wsframe"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
	"google.golang.org/protobuf/proto"
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
	byUID  map[string]map[string]*Conn
	rdb    *redis.Client
	gwID   string
}

func newHub(rdb *redis.Client, gwID string) *Hub {
	return &Hub{
		byConn: map[string]*Conn{},
		byUID:  map[string]map[string]*Conn{},
		rdb:    rdb,
		gwID:   gwID,
	}
}

func (h *Hub) bind(c *Conn) *Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.byConn[c.id] = c
	if h.byUID[c.uid] == nil {
		h.byUID[c.uid] = map[string]*Conn{}
	}
	h.byUID[c.uid][c.id] = c
	return nil
}

func (h *Hub) unbind(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.byConn[c.id]; ok && cur == c {
		delete(h.byConn, c.id)
	}
	if set := h.byUID[c.uid]; set != nil {
		if cur, ok := set[c.id]; ok && cur == c {
			delete(set, c.id)
		}
		if len(set) == 0 {
			delete(h.byUID, c.uid)
		}
	}
}

func (h *Hub) isOnline(uid string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byUID[uid]) > 0
}

func (h *Hub) push(gp *imv1.GatewayPush) {
	if gp == nil {
		return
	}
	h.mu.Lock()
	var targets []*Conn
	if gp.ConnId != "" {
		if c := h.byConn[gp.ConnId]; c != nil && (gp.Uid == "" || c.uid == gp.Uid) {
			targets = append(targets, c)
		} else if gp.Uid != "" {
			for _, c := range h.byUID[gp.Uid] {
				targets = append(targets, c)
			}
		}
	} else if gp.Uid != "" {
		for _, c := range h.byUID[gp.Uid] {
			targets = append(targets, c)
		}
	}
	h.mu.Unlock()
	for _, c := range targets {
		switch {
		case gp.Push != nil:
			c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Push{Push: gp.Push}})
		case gp.Typing != nil:
			c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Typing{Typing: gp.Typing}})
		case gp.Recalled != nil:
			c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Recalled{Recalled: gp.Recalled}})
		case gp.Read != nil:
			c.enqueue(&imv1.Envelope{Body: &imv1.Envelope_Read{Read: gp.Read}})
		}
	}
}

func (c *Conn) enqueue(env *imv1.Envelope) {
	select {
	case c.send <- env:
	default:
		log.Printf("conn %s send queue full, drop %T", c.id, env.Body)
	}
}

func (h *Hub) pushRoster(toUID, fromUID, kind, extra string) {
	toUID = strings.TrimSpace(toUID)
	fromUID = strings.TrimSpace(fromUID)
	if toUID == "" || toUID == fromUID {
		return
	}
	text := strings.TrimSpace(kind)
	if extra = strings.TrimSpace(extra); extra != "" {
		text += " " + extra
	}
	if text == "" {
		return
	}
	h.push(&imv1.GatewayPush{
		Uid: toUID,
		Push: &imv1.Push{
			Cid:         "sys:roster",
			FromUid:     fromUID,
			Payload:     &imv1.Payload{Type: imv1.Payload_SYSTEM, Text: text},
			CreatedAtMs: time.Now().UnixMilli(),
		},
	})
}

func (h *Hub) registerRoute(ctx context.Context, c *Conn) error {
	if h.rdb == nil {
		return nil
	}
	rec := route.Record{GatewayID: h.gwID, ConnID: c.id, DeviceID: c.deviceID}
	key := route.Key(c.uid)
	raw, err := h.rdb.Get(ctx, key).Bytes()
	if err != nil && err != redis.Nil {
		return err
	}
	b, err := route.Upsert(raw, rec)
	if err != nil {
		return err
	}
	return h.rdb.Set(ctx, key, b, route.TTL).Err()
}

func (h *Hub) refreshRoute(ctx context.Context, c *Conn) {
	if h.rdb == nil {
		return
	}
	if err := h.rdb.Expire(ctx, route.Key(c.uid), route.TTL).Err(); err != nil {
		log.Printf("refresh route %s: %v", c.uid, err)
	}
}

func (h *Hub) clearRoute(ctx context.Context, c *Conn) {
	if h.rdb == nil {
		return
	}
	key := route.Key(c.uid)
	raw, err := h.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return
	}
	b, err := route.Remove(raw, h.gwID, c.id)
	if err != nil {
		return
	}
	if b == nil {
		_ = h.rdb.Del(ctx, key).Err()
		return
	}
	_ = h.rdb.Set(ctx, key, b, route.TTL).Err()
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
	if h.rdb == nil {
		return
	}
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
			if strings.Contains(msg.Payload, `"kick"`) {
				var km struct {
					UID    string `json:"uid"`
					ConnID string `json:"conn_id"`
					Kick   string `json:"kick"`
				}
				if json.Unmarshal([]byte(msg.Payload), &km) == nil && km.Kick != "" {
					h.kickLocal(km.UID, km.ConnID)
					continue
				}
			}
			if err := unmarshalGatewayPush([]byte(msg.Payload), gp); err != nil {
				log.Printf("push decode: %v", err)
				continue
			}
			h.push(gp)
		}
	}
}

func unmarshalGatewayPush(b []byte, gp *imv1.GatewayPush) error {
	if err := proto.Unmarshal(b, gp); err == nil && gp.Uid != "" {
		return nil
	}
	return json.Unmarshal(b, gp)
}
