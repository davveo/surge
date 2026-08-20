package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func login(uid string) string {
	body, _ := json.Marshal(map[string]string{"uid": uid, "device_id": "smoke"})
	resp, err := http.Post("http://127.0.0.1:8080/v1/auth/dev-login", "application/json", bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Token string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		panic(fmt.Sprintf("login %s: %s", uid, raw))
	}
	return out.Token
}

func addFriend(token, peer string) {
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/friends", strings.NewReader(`{"peer_uid":"`+peer+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		panic(fmt.Sprintf("add friend: %s %s", resp.Status, raw))
	}
}

func connect(token string) *websocket.Conn {
	c, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8080/v1/ws", nil)
	if err != nil {
		panic(err)
	}
	if err := c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"auth":{"accessToken":%q,"deviceId":"smoke"}}`, token))); err != nil {
		panic(err)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		panic(err)
	}
	if !bytes.Contains(msg, []byte(`"authOk"`)) && !bytes.Contains(msg, []byte(`"auth_ok"`)) {
		panic("auth failed: " + string(msg))
	}
	return c
}

func readJSON(c *websocket.Conn) map[string]interface{} {
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		panic(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(msg, &m); err != nil {
		panic(string(msg))
	}
	return m
}

func sendText(c *websocket.Conn, id, peer, text string) map[string]interface{} {
	frame := fmt.Sprintf(`{"requestId":1,"send":{"clientMsgId":%q,"peerUid":%q,"payload":{"type":"TEXT","text":%q}}}`, id, peer, text)
	if err := c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		panic(err)
	}
	return readJSON(c)
}

func main() {
	t1 := login("u1")
	t2 := login("u2")
	a := connect(t1)
	b := connect(t2)
	defer a.Close()
	defer b.Close()

	denied := sendText(a, "m-stranger", "u2", "should fail")
	if _, ok := denied["error"]; !ok {
		fmt.Fprintf(os.Stderr, "expected not-friends error, got %#v\n", denied)
		os.Exit(1)
	}

	addFriend(t1, "u2")
	ack := sendText(a, "m-smoke-1", "u2", "hello p0")
	if _, ok := ack["ack"]; !ok {
		fmt.Fprintf(os.Stderr, "expected ack, got %#v\n", ack)
		os.Exit(1)
	}
	push := readJSON(b)
	if _, ok := push["push"]; !ok {
		fmt.Fprintf(os.Stderr, "expected push, got %#v\n", push)
		os.Exit(1)
	}
	fmt.Println("P0 smoke ok (friends required)")
	fmt.Printf("denied=%s\n", mustJSON(denied))
	fmt.Printf("ack=%s\n", mustJSON(ack))
	fmt.Printf("push=%s\n", mustJSON(push))
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
