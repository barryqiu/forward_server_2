package main

import (
    "flag"
    "github.com/gorilla/websocket"
    "net/http"
    "log"
    "strings"
    "net"
    "time"
    "fmt"
    "encoding/json"
)

var (
    address = flag.String("addr", ":5001", "http service address")

    upGrader = websocket.Upgrader{ReadBufferSize:4096, WriteBufferSize:40960} // use default options

    sendVRequestContent = `{"msgtype":"startdata","data":"v"}`
    sendHRequestContent = `{"msgtype":"startdata","data":"h"}`
    stopRequestContent = `{"msgtype":"stopdata","data":""}`
)

const (
    // Time allowed to write a message to the peer.
    writeWait = 10 * time.Second

    // Time allowed to read the next pong message from the peer.
    pongWait = 5 * time.Second

    // Send pings to peer with this period. Must be less than pongWait.
    pingPeriod = (pongWait * 9) / 10
)

type ClientParam struct {
    DeviceType string `json:"type"`
    Token      string `json:"token"`
}

func getSendContent(device_type string) string {
    if device_type == "h" {
        return sendHRequestContent
    } else {
        return sendVRequestContent
    }
}

type ClientConn struct {
    alias string
    ws    *websocket.Conn
    stop  chan int
    send  chan []byte
}


// readPump pumps messages from the websocket connection to the hub.
func (c *ClientConn) readPump() {
    defer func() {
        c.ws.Close()
        c.stop <- 1
    }()
    c.ws.SetReadDeadline(time.Now().Add(pongWait))
    c.ws.SetPongHandler(func(string) error {
        c.ws.SetReadDeadline(time.Now().Add(pongWait)); return nil
    })
    for {
        _, _, err := c.ws.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
                log.Printf("error: %v", err)
            }
            log.Printf("%v c.ws.ReadMessage", err)
            break
        }
    }
}

// write writes a message with the given message type and payload.
func (c *ClientConn) write(mt int, payload []byte) error {
    c.ws.SetWriteDeadline(time.Now().Add(writeWait))
    return c.ws.WriteMessage(mt, payload)
}

// writePump pumps messages from the hub to the websocket connection.
func (c *ClientConn) writePump() {
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        c.ws.Close()
        c.stop <- 1
    }()
    for {
        select {
        case message, ok := <-c.send:
            if !ok {
                // The hub closed the channel.
                log.Printf("%v c.send not ok", ok)
                c.write(websocket.CloseMessage, []byte{})
                return
            }

            c.ws.SetWriteDeadline(time.Now().Add(writeWait))
            w, err := c.ws.NextWriter(websocket.BinaryMessage)
            if err != nil {
                log.Printf("%v NextWriter", err)
                return
            }

            w.Write(message)

            if err := w.Close(); err != nil {
                log.Printf("%v w.Close", err)
                return
            }
        case <-ticker.C:
            if err := c.write(websocket.PingMessage, []byte{}); err != nil {
                log.Printf("%v write ping message", err)
                return
            }
            log.Printf("%v tick", c.alias)
            if (c.alias != "") {
                // 判断地址是否还存在，如果不存在则应该停止WS
                device_map, err := trans_phone_address(c.alias)
                if _, ok := phones[device_map]; err != nil || !ok {
                    log.Printf("phone map not exist, clost ws %v", c.alias)
                    return
                }
            }
        }
    }
}

func get_screen(w http.ResponseWriter, req *http.Request) {
    req.Header["Origin"] = nil
    conn, err := upGrader.Upgrade(w, req, nil)
    if err != nil {
        log.Print("upgrade:", err)
        conn.Close()
        return
    }
    log.Println("receive client conn from address", conn.RemoteAddr().String())
    conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"init","width":960,"height":540}`))

    device_type := "v"
    conn.SetReadDeadline(time.Now().Add(10 * time.Second))
    var clientParam ClientParam
    for {
        _, message, err := conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
                log.Printf("error: %v", err)
            }
            break
        }
        err = json.Unmarshal(message, &clientParam)
        device_type = string(clientParam.DeviceType)
        break
    }

    uri := req.RequestURI
    log.Println("URI:", uri)
    infos := strings.Split(uri, "/")
    if (len(infos) <= 1) {
        log.Println("wrong url")
        conn.Close()
        return
    }
    device_name := infos[1]
    alias := ""

    if _, ok := phones[device_name]; !ok {
        device_map, err := trans_phone_address(device_name)
        if _, ok := phones[device_map]; err == nil && ok {
            alias = device_name
            device_name = device_map
        } else {
            log.Println(device_name + " not exist")
            conn.Close()
            return
        }
    }

    //ws_state := get_phone_ws_state_in_redis(device_name)
    //if ws_state == 1{
    //	phones[device_name].log_to_file(device_name + " is in use")
    //	conn.WriteMessage(websocket.TextMessage, []byte("device is in use"))
    //	conn.Close()
    //	return
    //}

    phones[device_name].log_to_file(fmt.Sprintf("param : %v", clientParam))
    //log.Printf("param : %v\n", clientParam)

    clientConn := &ClientConn{send: make(chan []byte, 4096), ws: conn, stop: make(chan int), alias: alias}

    go clientConn.writePump()
    go clientConn.readPump()

    if (phones[device_name].Conn == net.TCPConn{}) {
        phones[device_name].log_to_file("no phone conn error:", err)
        conn.Close()
        return
    }
    set_phone_ws_state_in_redis(device_name, 1)
    phones[device_name].Client_conn = clientConn
    phones[device_name].WriteMsgToDevice([]byte(getSendContent(device_type)), 1)

    for {
        select {
        case stop := <-clientConn.stop:
            if stop == 1 {
                clientConn.ws = nil
                phones[device_name].log_to_file("client close, stop fetch data")
                set_phone_ws_state_in_redis(device_name, 0)
                phones[device_name].WriteMsgToDevice([]byte(stopRequestContent), 1)
                return
            }
        }
    }
}

func start_ws() {
    http.HandleFunc("/", get_screen)
    log.Println("listen web socket 5001 success")
    http.ListenAndServe(*address, nil)
}
