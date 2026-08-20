package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/davveo/surge/pkg/auth"
	"github.com/davveo/surge/pkg/wsframe"
	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type wsServer struct {
	hub    *Hub
	core   imv1.IMCoreClient
	secret string
	idle   time.Duration
}

func (s *wsServer) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade: %v", err)
		return
	}
	c := newConn(ws, s.hub)
	go c.writeLoop()
	s.readLoop(c)
}

func (s *wsServer) readLoop(c *Conn) {
	defer func() {
		s.hub.unbind(c)
		if c.uid != "" {
			s.hub.clearRoute(context.Background(), c)
		}
		close(c.send)
		c.ws.Close()
	}()

	c.ws.SetReadLimit(64 << 10)
	_ = c.ws.SetReadDeadline(time.Now().Add(s.idle))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(s.idle))
	})

	for {
		msgType, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(s.idle))
		isBinary := msgType == websocket.BinaryMessage
		env, err := wsframe.Decode(isBinary, data)
		if err != nil {
			c.enqueue(errEnv(0, 400, err.Error(), ""))
			continue
		}
		c.binary = isBinary
		s.dispatch(c, env)
	}
}

func (s *wsServer) dispatch(c *Conn, env *imv1.Envelope) {
	switch body := env.Body.(type) {
	case *imv1.Envelope_Auth:
		s.onAuth(c, env.RequestId, body.Auth)
	case *imv1.Envelope_Heartbeat:
		if c.uid != "" {
			s.hub.refreshRoute(context.Background(), c)
		}
		c.enqueue(&imv1.Envelope{
			RequestId: env.RequestId,
			Body:      &imv1.Envelope_Heartbeat{Heartbeat: &imv1.Heartbeat{TsMs: time.Now().UnixMilli()}},
		})
	case *imv1.Envelope_Send:
		s.onSend(c, env.RequestId, body.Send)
	case *imv1.Envelope_Sync:
		s.onSync(c, env.RequestId, body.Sync)
	default:
		c.enqueue(errEnv(env.RequestId, 400, "unsupported frame", ""))
	}
}

func (s *wsServer) onAuth(c *Conn, reqID uint64, req *imv1.AuthRequest) {
	claims, err := auth.Parse(s.secret, req.GetAccessToken())
	if err != nil {
		c.enqueue(errEnv(reqID, 401, "unauthorized", ""))
		return
	}
	c.uid = claims.Subject
	c.deviceID = req.GetDeviceId()
	if c.deviceID == "" {
		c.deviceID = claims.DeviceID
	}
	if kicked := s.hub.bind(c); kicked != nil {
		kicked.enqueue(errEnv(0, 409, "kicked by another connection", ""))
		_ = kicked.ws.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.hub.registerRoute(ctx, c); err != nil {
		log.Printf("register route: %v", err)
		c.enqueue(errEnv(reqID, 500, "route failed", ""))
		return
	}
	wm, err := s.core.Watermark(ctx, &imv1.WatermarkRequest{Uid: c.uid})
	if err != nil {
		log.Printf("watermark: %v", err)
		wm = &imv1.WatermarkResponse{}
	}
	c.enqueue(&imv1.Envelope{
		RequestId: reqID,
		Body: &imv1.Envelope_AuthOk{AuthOk: &imv1.AuthResponse{
			UserId:      c.uid,
			DeviceId:    c.deviceID,
			GatewayId:   s.hub.gwID,
			ConnId:      c.id,
			LastSyncSeq: wm.GetLastSyncSeq(),
		}},
	})
}

func (s *wsServer) onSend(c *Conn, reqID uint64, req *imv1.SendRequest) {
	if c.uid == "" {
		c.enqueue(errEnv(reqID, 401, "auth required", req.GetClientMsgId()))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := s.core.Send(ctx, &imv1.SendMessageRequest{
		FromUid:     c.uid,
		ClientMsgId: req.GetClientMsgId(),
		Cid:         req.GetCid(),
		PeerUid:     req.GetPeerUid(),
		Payload:     req.GetPayload(),
	})
	if err != nil {
		code := 500
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				code = 400
			case codes.PermissionDenied:
				code = 403
			}
		}
		c.enqueue(errEnv(reqID, int32(code), err.Error(), req.GetClientMsgId()))
		return
	}
	c.enqueue(&imv1.Envelope{
		RequestId: reqID,
		Body:      &imv1.Envelope_Ack{Ack: resp.Ack},
	})
}

func (s *wsServer) onSync(c *Conn, reqID uint64, req *imv1.SyncRequest) {
	if c.uid == "" {
		c.enqueue(errEnv(reqID, 401, "auth required", ""))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := s.core.Sync(ctx, &imv1.SyncInboxRequest{
		Uid:         c.uid,
		LastSyncSeq: req.GetLastSyncSeq(),
		Limit:       req.GetLimit(),
	})
	if err != nil {
		c.enqueue(errEnv(reqID, 500, err.Error(), ""))
		return
	}
	c.enqueue(&imv1.Envelope{
		RequestId: reqID,
		Body:      &imv1.Envelope_SyncResp{SyncResp: resp.GetSync()},
	})
}

func errEnv(reqID uint64, code int32, msg, clientMsgID string) *imv1.Envelope {
	return &imv1.Envelope{
		RequestId: reqID,
		Body: &imv1.Envelope_Error{Error: &imv1.Error{
			Code:        code,
			Message:     msg,
			ClientMsgId: clientMsgID,
		}},
	}
}
