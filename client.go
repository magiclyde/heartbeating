// +build ignore

package main

import (
	"flag"
	"github.com/gorilla/websocket"
	"github.com/magiclyde/tuna/jwt"
	"github.com/magiclyde/tuna/logger"
	"go.uber.org/zap"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var addr = flag.String("addr", "localhost:9999", "http service address")
var num = flag.Int("num", 1e3, "num")
var jwtKey = flag.String("jwtKey", "changeme", "jwt key")
var logFile = flag.String("logFile", "", "log file")
var sugar = logger.NewLogger(zap.InfoLevel, *logFile).Sugar()

func main() {
	flag.Parse()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	for i := 1; i <= *num; i++ {
		wg.Add(1)
		go func(i int, interrupt chan os.Signal) {
			defer wg.Done()
			newUser(strconv.Itoa(i), interrupt)
		}(i, interrupt)
	}
	wg.Wait()
}

func newUser(uid string, interrupt chan os.Signal) {
	ts := time.Now().Unix()

	v := url.Values{}
	v.Set("uid", uid)
	v.Set("ts", strconv.FormatInt(ts, 10))
	endpoint := url.URL{
		Scheme:   "ws",
		Host:     *addr,
		Path:     "/ws",
		RawQuery: v.Encode(),
	}

	mp := make(map[string]interface{})
	mp["uid"] = uid
	mp["ts"] = ts
	token, err := jwt.CreateToken(*jwtKey, mp)
	if err != nil {
		sugar.Fatalf("[client:%s] jwt.CreateToken.err, %v", uid, err)
	}

	header := http.Header{}
	header.Set("token", token)

	c, _, err := websocket.DefaultDialer.Dial(endpoint.String(), header)
	if err != nil {
		sugar.Fatalf("[client:%s] dial.err, %v", uid, err)
	}
	defer c.Close()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case t := <-ticker.C:
			err := c.WriteMessage(websocket.PingMessage, []byte(t.String()))
			if err != nil {
				sugar.Infof("[client:%s] write.err, %v", uid, err)
				return
			}

		case next := <-interrupt:
			sugar.Infof("[client:%s] interrupt", uid)

			// 通知下一个 client 退出
			interrupt <- next

			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				sugar.Infof("[client:%s] write close: %v", uid, err)
				return
			}
		}
	}

}
