package main

import (
  "crypto/hmac"
  "crypto/sha512"
  "crypto/tls"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "io"
  "net/url"
  "time"

  "github.com/gorilla/websocket"
)

type Msg struct {
  Time    int64    `json:"time"`
  Channel string   `json:"channel"`
  Event   string   `json:"event"`
  Payload []string `json:"payload"`
  Auth    *Auth    `json:"auth"`
}

type Auth struct {
  Method string `json:"method"`
  KEY    string `json:"KEY"`
  SIGN   string `json:"SIGN"`
}

const (
  Key    = "YOUR_API_KEY"
  Secret = "YOUR_API_SECRETY"
)

func sign(channel, event string, t int64) string {
  message := fmt.Sprintf("channel=%s&event=%s&time=%d", channel, event, t)
  h2 := hmac.New(sha512.New, []byte(Secret))
  io.WriteString(h2, message)
  return hex.EncodeToString(h2.Sum(nil))
}

func (msg *Msg) sign() {
  signStr := sign(msg.Channel, msg.Event, msg.Time)
  msg.Auth = &Auth{
    Method: "api_key",
    KEY:    Key,
    SIGN:   signStr,
  }
}

func (msg *Msg) send(c *websocket.Conn) error {
  msgByte, err := json.Marshal(msg)
  if err != nil {
    return err
  }
  return c.WriteMessage(websocket.TextMessage, msgByte)
}

func NewMsg(channel, event string, t int64, payload []string) *Msg {
  return &Msg{
    Time:    t,
    Channel: channel,
    Event:   event,
    Payload: payload,
  }
}

func main() {
  u := url.URL{Scheme: "wss", Host: "fx-ws.gateio.ws", Path: "/v4/ws/usdt"}
  websocket.DefaultDialer.TLSClientConfig = &tls.Config{RootCAs: nil, InsecureSkipVerify: true}
  c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
  if err != nil {
    panic(err)
  }
  c.SetPingHandler(nil)

  // read msg
  go func() {
    for {
      _, message, err := c.ReadMessage()
      if err != nil {
        c.Close()
        panic(err)
      }
      fmt.Printf("recv: %s\n", message)
    }
  }()

  t := time.Now().Unix()
  pingMsg := NewMsg("futures.ping", "", t, []string{})
  err = pingMsg.send(c)
  if err != nil {
    panic(err)
  }

  // subscribe order book
  orderBookMsg := NewMsg("futures.order_book", "subscribe", t, []string{"BTC_USDT"})
  err = orderBookMsg.send(c)
  if err != nil {
    panic(err)
  }

  // subscribe positions
  positionsMsg := NewMsg("futures.positions", "subscribe", t, []string{"USERID", "BTC_USDT"})
  positionsMsg.sign()
  err = positionsMsg.send(c)
  if err != nil {
    panic(err)
  }

  select {}
}