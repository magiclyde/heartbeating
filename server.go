package main

import (
	"flag"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/magiclyde/tuna/logger"
	"github.com/magiclyde/tuna/middleware"
	"github.com/syyongx/php2go"
	"go.uber.org/zap"
	"net"
	"net/http"
	"time"
)

const (
	// Time allowed to read the next ping message from the client.
	// Client send pings to server with period must be less this pingWait.
	pingWait = 60 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var (
	addr     = flag.String("addr", ":9999", "http service address")
	mode     = flag.String("mode", gin.DebugMode, "debug mode")
	jwtKey   = flag.String("jwtKey", "changeme", "jwt key")
	logFile  = flag.String("logFile", "server.log", "log file")
	sugar    = logger.NewLogger(zap.InfoLevel, *logFile).Sugar()
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	users    = make(map[string]bool)
	entering = make(chan string)
	leaving  = make(chan string)
)

type Params struct {
	Uid string `form:"uid" binding:"required"`
	Ts  int    `form:"ts" binding:"required"`
}

type User struct {
	uid  string
	conn *websocket.Conn
}

func (u *User) handleConn() {
	defer func() {
		u.conn.Close()
		u.leaving()
	}()

	u.entering()
	u.conn.SetReadLimit(maxMessageSize)
	u.conn.SetReadDeadline(time.Now().Add(pingWait))
	u.conn.SetPingHandler(u.pingHandler())

	for {
		_, _, err := u.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				sugar.Errorf("websocket.IsUnexpectedCloseError: %v", err)
			}
			break
		}
	}
}

func (u *User) entering() {
	entering <- u.uid
}

func (u *User) leaving() {
	leaving <- u.uid
}

// send pongs to peer
func (u *User) pingHandler() func(message string) error {
	h := func(message string) error {
		u.conn.SetReadDeadline(time.Now().Add(pingWait))

		err := u.conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(time.Second))
		if err == websocket.ErrCloseSent {
			return nil
		} else if e, ok := err.(net.Error); ok && e.Temporary() {
			return nil
		}
		return err
	}
	return h
}

func serveWs(c *gin.Context) {
	var params Params
	if err := c.ShouldBindQuery(&params); err != nil {
		sugar.Errorf("miss params: %v", err)
		return
	}

	upgrader.CheckOrigin = func(r *http.Request) bool {
		return true
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		if _, ok := err.(websocket.HandshakeError); !ok {
			sugar.Errorf("websocket.HandshakeError: %v", err)
		}
		return
	}

	user := User{uid: params.Uid, conn: conn}
	go user.handleConn()
}

func hub() {
	for {
		select {
		case uid := <-entering:
			sugar.Infof("uid: %s entering", uid)
			users[uid] = true

		case uid := <-leaving:
			sugar.Infof("uid: %s leaving", uid)
			delete(users, uid)
		}
	}
}

func ipLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !php2go.InArray(c.ClientIP(), []string{"127.0.0.1", "::1"}) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

func main() {
	flag.Parse()

	gin.SetMode(*mode)
	router := gin.New()
	router.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{"/favicon.ico"},
	}))
	router.Use(gin.Recovery())

	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// profiler
	if *mode == gin.DebugMode {
		group := router.Group("/debug", ipLimitMiddleware())
		pprof.RouteRegister(group, "pprof")
	}

	// guard
	router.Use(middleware.Authorization(*jwtKey))

	// handle websocket conn
	go hub()
	router.GET("/ws", serveWs)

	sugar.Fatal(router.Run(*addr))
}
