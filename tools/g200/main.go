package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "gateway base URL")
	n := flag.Int("n", 200, "group size including owner")
	msgs := flag.Int("msgs", 10, "messages the owner sends")
	maxP99 := flag.Int("max-p99-ms", 0, "fail if ACK p99 exceeds this (0=report only)")
	flag.Parse()
	if *n < 3 {
		*n = 3
	}
	if *n > 200 {
		*n = 200
	}

	owner := "g200-owner"
	members := make([]string, 0, *n-1)
	for i := 1; i < *n; i++ {
		members = append(members, fmt.Sprintf("g200-u%03d", i))
	}

	fmt.Printf("login %d users…\n", *n)
	ownerTok := login(*base, owner)
	toks := make([]string, len(members))
	var wg sync.WaitGroup
	for i, uid := range members {
		i, uid := i, uid
		wg.Add(1)
		go func() {
			defer wg.Done()
			toks[i] = login(*base, uid)
		}()
	}
	wg.Wait()

	fmt.Printf("add %d friends…\n", len(members))
	for _, uid := range members {
		addFriend(*base, ownerTok, uid)
	}

	fmt.Println("create group…")
	cid := createGroup(*base, ownerTok, "g200", members)
	fmt.Println("cid", cid)

	fmt.Println("connect websockets…")
	ownerWS := connectWS(*base, ownerTok)
	defer ownerWS.Close()
	memberWS := make([]*websocket.Conn, len(members))
	for i, tok := range toks {
		c := connectWS(*base, tok)
		memberWS[i] = c
		defer c.Close()
	}

	var pushes atomic.Int64
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = memberWS[0].SetReadDeadline(time.Now().Add(30 * time.Second))
				_, msg, err := memberWS[0].ReadMessage()
				if err != nil {
					return
				}
				if bytes.Contains(msg, []byte(`"push"`)) {
					pushes.Add(1)
				}
			}
		}
	}()

	lat := make([]time.Duration, 0, *msgs)
	for i := 0; i < *msgs; i++ {
		id := fmt.Sprintf("g200-%d-%d", time.Now().UnixNano(), i)
		start := time.Now()
		ack := sendGroup(ownerWS, id, cid, fmt.Sprintf("load %d", i))
		if _, ok := ack["ack"]; !ok {
			fmt.Fprintf(os.Stderr, "lost ACK on msg %d: %#v\n", i, ack)
			os.Exit(1)
		}
		lat = append(lat, time.Since(start))
	}
	close(done)
	time.Sleep(300 * time.Millisecond)

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p50 := lat[len(lat)*50/100]
	p99 := lat[(len(lat)*99)/100]
	if p99 < lat[len(lat)-1] && len(lat) >= 100 {
		p99 = lat[len(lat)*99/100]
	} else if len(lat) < 100 {
		p99 = lat[len(lat)-1]
	}
	fmt.Printf("g200 ok n=%d msgs=%d ack_p50=%s ack_p99=%s sample_pushes=%d\n",
		*n, *msgs, p50, p99, pushes.Load())
	if *maxP99 > 0 && p99 > time.Duration(*maxP99)*time.Millisecond {
		fmt.Fprintf(os.Stderr, "p99 %s exceeds %dms\n", p99, *maxP99)
		os.Exit(2)
	}
}

func login(base, uid string) string {
	body, _ := json.Marshal(map[string]string{"uid": uid, "device_id": "g200"})
	resp, err := http.Post(strings.TrimRight(base, "/")+"/v1/auth/dev-login", "application/json", bytes.NewReader(body))
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

func addFriend(base, token, peer string) {
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/v1/friends", strings.NewReader(`{"peer_uid":"`+peer+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		panic(fmt.Sprintf("add friend %s: %s %s", peer, resp.Status, raw))
	}
}

func createGroup(base, token, name string, members []string) string {
	body, _ := json.Marshal(map[string]interface{}{"name": name, "members": members})
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/v1/groups", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		panic(fmt.Sprintf("create group: %s %s", resp.Status, raw))
	}
	var out struct {
		CID string `json:"cid"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.CID == "" {
		panic("create group parse: " + string(raw))
	}
	return out.CID
}

func connectWS(base, token string) *websocket.Conn {
	u := strings.Replace(strings.TrimRight(base, "/"), "http://", "ws://", 1)
	u = strings.Replace(u, "https://", "wss://", 1) + "/v1/ws"
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		panic(err)
	}
	if err := c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"auth":{"accessToken":%q,"deviceId":"g200"}}`, token))); err != nil {
		panic(err)
	}
	c.SetReadDeadline(time.Now().Add(8 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		panic(err)
	}
	if !bytes.Contains(msg, []byte(`"authOk"`)) && !bytes.Contains(msg, []byte(`"auth_ok"`)) {
		panic("auth failed: " + string(msg))
	}
	return c
}

func sendGroup(c *websocket.Conn, id, cid, text string) map[string]interface{} {
	frame := fmt.Sprintf(`{"requestId":1,"send":{"clientMsgId":%q,"cid":%q,"payload":{"type":"TEXT","text":%q}}}`, id, cid, text)
	if err := c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		panic(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c.SetReadDeadline(deadline)
		_, msg, err := c.ReadMessage()
		if err != nil {
			panic(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(msg, &m); err != nil {
			panic(string(msg))
		}
		if _, ok := m["ack"]; ok {
			return m
		}
		if _, ok := m["error"]; ok {
			return m
		}
	}
	panic("timeout waiting ack")
}
