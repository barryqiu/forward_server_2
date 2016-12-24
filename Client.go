package main

import (
    "log"
    "net"
    "bytes"
    "strings"
    "os"
)
// 策略:
// 使用一个全局的slice数组存储所有的Phone
// 接受客户端请求的部分使用和接受云端设备请求的部分使用Go的socket编程处理,
// 同样使用全局变量来操作云端设备对象,实现添加链接等操作
// 测试链接是否可用：/{device_name}/testconn
var headerHtml = `HTTP/1.1 200 OK
Cache-Control: no-store, no-cache, must-revalidate
Cache-Control: post-check=0, pre-check=0
Pragma: no-cache
Connection: close

`

func renderHtmlFileAndClose(conn net.TCPConn, tpl string) {
    defer conn.Close()

    //file to read
    file, err := os.Open(strings.TrimSpace(tpl)) // For read access.
    if err != nil {

        log.Fatal(err)
    }

    defer file.Close()
    buf := make([]byte, 4096)
    conn.Write([]byte(headerHtml))
    for {
        n, _ := file.Read(buf)
        if 0 == n {
            break
        }
        conn.Write(buf[:n])
    }
    return
}

func processClientReq(conn net.TCPConn) {

    if (net.TCPConn{}) == conn {
        return
    }

    log.Println("receive client conn from address", conn.RemoteAddr().String())

    defer conn.Close()

    var content []byte
    var header []byte
    var body []byte
    for {
        var buf = make([]byte, 4096)
        n, err := conn.Read(buf)
        if err != nil {
            log.Println("cleint conn read error:", err)
            renderHtmlFileAndClose(conn, "net_error.html")
            return
        }
        content = append(content, buf[:n]...)
        header_end := bytes.Index(content, []byte("\r\n\r\n"))
        if (header_end != -1) {
            header = content[:header_end + 4]
            body = content[header_end + 4:]
            break
        }
    }

    req, err := getRequestInfo(string(header))
    if err != nil {
        log.Println("client conn wrong request format:", err)
        renderHtmlFileAndClose(conn, "net_error.html")
        return
    }
    for len(body) < int(req.ContentLength) {
        var buf = make([]byte, 4096)
        n, err := conn.Read(buf)
        if err != nil {
            log.Println("conn read error:", err)
            renderHtmlFileAndClose(conn, "net_error.html")
            return
        }
        body = append(body, buf[:n]...)

    }
    uri := req.RequestURI
    log.Println("URI:", uri)
    infos := strings.Split(uri, "/")
    if (len(infos) <= 1) {
        renderHtmlFileAndClose(conn, "net_error.html")
        log.Println("wrong url")
        return
    }
    device_name := infos[1]

    if _, ok := phones[device_name]; !ok {
        device_map, err := trans_phone_address(device_name)
        if _, ok := phones[device_map]; err == nil && ok {
            device_name = device_map
        } else {
            renderHtmlFileAndClose(conn, "404.html")
            log.Println(device_name + " not exist")
            return
        }
    }

    // 渲染页面
    if len(infos) > 2 && strings.HasPrefix(infos[2], "phone.html") {
        renderHtmlFileAndClose(conn, "phone.html")
        phones[device_name].log_to_file(device_name + " phone.html")
        return
    }

    // if uri like /device_name/touch
    // 向设备端发送触摸动作
    if len(infos) > 2 && strings.HasPrefix(infos[2], "touch") {
        phones[device_name].WriteMsgToDevice(body, 2)
        phones[device_name].log_to_file("touch", string(body))
        conn.Write([]byte(headerHtml))
    }

    // if uri like /device_name/ctrl
    // 像设备端发送其他控制信息
    if len(infos) > 2 && strings.HasPrefix(infos[2], "ctrl") {
        phones[device_name].WriteMsgToDevice(body, 2)
        phones[device_name].log_to_file("ctrl", string(body))
        conn.Write([]byte(headerHtml))
    }
}