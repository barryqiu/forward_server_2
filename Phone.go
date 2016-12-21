package main

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
    "time"
    "strconv"
    "bytes"
    "encoding/json"
)

type Phone struct {
	mu          sync.Mutex
	Conn        net.TCPConn
	Device_name string
	Random      string
	Last_known  string
    Client_conn *ClientConn
	Data_client chan []byte
	Data_device chan []byte
	Stop        chan int
	CloseConn   chan int
}

type DeviceMsg struct  {
    MsgType string `json:"msgtype"`
    Content string `json:"content"`

}

func ReceivePhoneData(phone *Phone) {

}

func ProcessDevicePackage(phone *Phone, data []byte, head_length int)  {
    str_head := string(data[:head_length])
    body := data[head_length:]
    str_type := str_head[3:4]
    if(str_type == "1"){
        var deviceMsg DeviceMsg
        err := json.Unmarshal(body, &deviceMsg)
        if err != nil{
            return
        }
        if deviceMsg.MsgType == "heart"{
            phone.Conn.Write(body)
        }
    }else if str_type == "2"{
        fmt.Println(string(body), phone.Client_conn)
        phone.Client_conn.send <- body
    }
}

func ReadDataFromDevice(phone *Phone) {
    var content []byte
    pack_length := 0
    for ; ;  {
        if (net.TCPConn{}) == phone.Conn {
            fmt.Println("no conn sleep 1 second")
            time.Sleep(time.Second * 1)
            continue
        }
        var buf = make([]byte, 4096)
        n, err := phone.Conn.Read(buf)
        if err != nil || n == 0{
            log.Println("phone conn read error:", err)
            continue
        }
        content = append(content, buf[:n]...)

        fmt.Println(string(content))

        if (bytes.Index(content, []byte("\r\n\r\n")) < 0){
            continue
        }

        pack_start_index := bytes.Index(content, []byte("STP"))
        if pack_start_index != 0 {
            phone.Conn.Close()
            phone.Conn = net.TCPConn{}
            return
        }

        fmt.Println(string(content))

        head_length := bytes.Index(content, []byte("\r\n\r\n")) + len([]byte("\r\n\r\n"))
        headStr := string(content[:head_length])

        fmt.Println(headStr)

        // STP device_name/random
        lines := strings.Split(headStr, "\r\n")
        if len(lines) > 1 {
            length := lines[1]
            int_length, err := strconv.Atoi(length)
            if err != nil {
                phone.Conn.Close()
                phone.Conn = net.TCPConn{}
                return
            }
            pack_length = int_length
        }else {
            phone.Conn.Close()
            phone.Conn = net.TCPConn{}
            return
        }
        pack_length += head_length

        if (pack_length <= len(content)){
            ProcessDevicePackage(phone, content[:pack_length], head_length)
            content = content[pack_length:]
        }
    }
}

func (phone *Phone) append_conn(conn net.TCPConn) error {
	phone.mu.Lock()

    // 断开当前链接
    if (net.TCPConn{}) != phone.Conn {
        phone.Conn.Close()
        phone.Conn = net.TCPConn{}
    }

	err := conn.SetKeepAlive(true)
	if err != nil {
		phone.log_to_file("set keep alive error:", err)
	}
	phone.Conn = conn

	// 通知云端连接建立成功
	phone.Conn.Write([]byte("STP1\r\n0\r\n\r\n"))
	phone.log_to_file("new conn", conn.RemoteAddr().String())
	phone.mu.Unlock()
	return nil
}

func (phone *Phone) Listen()  {
    /**
	处理收到的各种 chan 信息
	*/
    go ReceivePhoneData(phone)

    /**
    从 device 端接受心跳信息
    */
    go ReadDataFromDevice(phone)

}

func (phone *Phone) add_to_file() error {
	fl, err := os.OpenFile(db_file_name, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0660)
	defer fl.Close()
	if err != nil {
		phone.log_to_file("open file error", err)
		return err
	}
	fl.WriteString(phone.Device_name + " " + phone.Random + "\n")
	return nil
}

func (phone *Phone) log_to_file(v ...interface{}) error {
	string_date := current_date_string()
	string_time := current_time_string()
	os.MkdirAll("log"+string(filepath.Separator)+string_date, 06660)
	log_file_name := "log" + string(filepath.Separator) + string_date + string(filepath.Separator) + phone.Device_name + ".log"
	fl, err := os.OpenFile(log_file_name, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0660)
	defer fl.Close()
	if err != nil {
		log.Println("open file error", err)
		return err
	}
	fl.WriteString("[" + string_time + "]" + fmt.Sprintln(v...))
	return nil
}

/**
trans phone  address
*/
func trans_phone_address(address_map string) (string, error) {
	redis_key := fmt.Sprintf("YUNPHONE:DEVICE:MAP:%s", address_map)
	redis_key = strings.ToUpper(redis_key)
	redis_conn, err := getRedisConn()
	defer redis_conn.Close()
	if err != nil {
		log.Println("REDIS CONN ERROR", redis_key, err)
		return "", err
	}
	device_name, err := redis.String(redis_conn.Do("GET", redis_key))
	if err != nil {
		log.Println("REDIS GET ERROR", redis_key, err)
		return "", err
	}
	log.Println("REDIS GET ", redis_key, ": ", device_name)
	return device_name, err
}

/**
trans phone  address
*/
func set_phone_ws_state_in_redis(phone_name string, state int) error {
	if _, ok := phones[phone_name]; !ok {
		return errors.New("phone not exist")
	}
	redis_key := fmt.Sprintf("YUNPHONE:DEVICE:WS:STATE:%s", phone_name)
	redis_key = strings.ToUpper(redis_key)
	redis_conn, err := getRedisConn()
	defer redis_conn.Close()
	if err != nil {
		phones[phone_name].log_to_file("REDIS CONN ERROR", redis_key, err)
		return err
	}
	_, err = redis.String(redis_conn.Do("SET", redis_key, state))
	if err != nil {
		phones[phone_name].log_to_file("REDIS SET ERROR", redis_key, err)
		return err
	}
	phones[phone_name].log_to_file("REDIS SET ", redis_key, ": ", phone_name, ":", state)
	return err
}

//func get_phone_ws_state_in_redis(phone_name string) int {
//	if _, ok := phones[phone_name]; !ok {
//		return 0
//	}
//	redis_key := fmt.Sprintf("YUNPHONE:DEVICE:WS:STATE:%s", phone_name)
//	redis_key = strings.ToUpper(redis_key)
//	redis_conn, err := getRedisConn()
//	defer redis_conn.Close()
//	if err != nil {
//		phones[phone_name].log_to_file("REDIS CONN ERROR", redis_key, err)
//		return 0
//	}
//	state, err := redis.Int(redis_conn.Do("GET", redis_key))
//	if err != nil {
//		phones[phone_name].log_to_file("REDIS GET ERROR", redis_key, err)
//		return 0
//	}
//	phones[phone_name].log_to_file("REDIS GET WS State ", redis_key, ": ", state)
//	return state
//}

/**
read phone‘s info from file
*/
func read_phones_from_file() {
	fl, err := os.Open(db_file_name)
	if err != nil {
		log.Println("open file error", err)
		return
	}
	defer fl.Close()

	scanner := bufio.NewScanner(fl)
	for scanner.Scan() {
		line := scanner.Text()
		log.Println(line)
		infos := strings.Split(line, " ")
		if len(infos) == 2 {
            phone := Phone{Device_name: infos[0], Random: infos[1],
                Data_client: make(chan []byte, 4096), Data_device: make(chan []byte, 4096),
                Stop: make(chan int), CloseConn:make(chan  int)}
            phone.Listen()
			phones[infos[0]] = &phone
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}

/**
start phone thread to listen request from yun phone
*/
func start_phones() {

	read_phones_from_file()

	add, err := net.ResolveTCPAddr("tcp", ":5110")
	if err != nil {
		log.Println("error listen:", err)
		return
	}
	listen, err := net.ListenTCP("tcp", add)
	if err != nil {
		log.Println("error listen:", err)
		return
	}
	defer listen.Close()
	log.Println("listen 5110 ok")

	for {
		conn, err := listen.AcceptTCP()
		if err != nil {
			log.Println("accept error:", err)
		}
		go process_phone_conn(*conn)
	}
}

func process_phone_conn(conn net.TCPConn) {
	if (net.TCPConn{}) == conn {
		return
	}
    var content []byte
	var buf = make([]byte, 4096)

    for ; ;  {
        n, err := conn.Read(buf)
        if err != nil {
            log.Println("phone conn read error:", err)
            conn.Close()
            return
        }
        content = append(content, buf[:n]...)
        if bytes.Index(content, []byte("\r\n\r\n")) > 0 {
           break
        }
    }

    content_str := string(content)


    // GET /register_/device_name/random/version
	if strings.HasPrefix(content_str, "GET /register_") {
		req, err := getRequestInfo(content_str)
		if err != nil {
			log.Println("phone conn read error:", err)
			return
		}
		infos := strings.Split(req.RequestURI, "/")
		device_name := infos[2]
		random := infos[3]
		version := infos[4]
		log.Println("reg:device_name:" + device_name + ";random:" + random + ";version:" + version)

		if len(device_name) == 0 {
			conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\nEmpty device name is not allowed."))
			conn.Close()
			return
		}

		if _, ok := phones[device_name]; ok && phones[device_name].Random != random {
			conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\nDevice name is already exists."))
			conn.Close()
			return
		}

		phone := Phone{Device_name: device_name, Random: random,
			Data_client: make(chan []byte, 4096), Data_device: make(chan []byte, 4096),
			Stop: make(chan int), CloseConn:make(chan  int)}
		phone.add_to_file()
        phone.Listen()
		phones[device_name] = &phone
		conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\nOK"))
		conn.Close()
		return
	}



	if strings.HasPrefix(content_str, "STP") {
		// STP device_name/random
		lines := strings.Split(content_str, "\r\n")
		if len(lines) > 0 {
			first_line := lines[0]
			p1 := strings.Index(first_line, "/")
			device_name := first_line[4:p1]
			random := first_line[p1+1:]

			if len(device_name) <= 0 || len(random) <= 0 {
				log.Println("device_name or random len = 0")
				conn.Write([]byte("stop"))
				conn.Close()
				return
			}

			_, ok := phones[device_name]

			if ok && (phones[device_name].Random == random) {
				//log.Println(user_name, " phone append a conn", conn.RemoteAddr().String())
				phones[device_name].append_conn(conn)
				return
			} else if !ok {
                phone := Phone{Device_name: device_name, Random: random,
                    Data_client: make(chan []byte, 4096), Data_device: make(chan []byte, 4096),
                    Stop: make(chan int), CloseConn:make(chan  int)}
				phone.add_to_file()
                phone.Listen()
				phones[device_name] = &phone
				phones[device_name].append_conn(conn)
				log.Println("new phone ", device_name)
			} else {
				log.Println(device_name, random, "stop phone old phone random: ", phones[device_name].Random)
				conn.Close()
				log.Println("no thing matched")
				return
			}
		}

	}

}
