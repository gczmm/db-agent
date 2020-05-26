package server

import (
	"bufio"
	"bytes"
	"github.com/Devying/db-agent/config"
	"github.com/Devying/db-agent/third_party/redigo/redis"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type Redis struct {
	Pool map[string]*redis.Pool
	Conf map[string]config.DBConf
	Port int
}
func (r *Redis)GetPort()int{
	return r.Port
}
func (r *Redis)GetPool(ins string)(*redis.Pool,error){
	if pool,ok := r.Pool[ins];ok{
		return pool,nil
	}
	return nil,errors.New("instance does not exist")
}
func (r *Redis)Initialize(){
	r.Pool = make(map[string]*redis.Pool)
	for ins,conf:= range r.Conf{
		r.Pool[ins] = &redis.Pool{
			MaxIdle:     500,
			IdleTimeout: 240 * time.Second,
			Dial: func() (redis.Conn, error) {
				c, err := redis.Dial("tcp", conf.Host+":"+strconv.Itoa(conf.Port))
				if err != nil {
					return nil, err
				}
				return c, err
			},

			TestOnBorrow: func(c redis.Conn, t time.Time) error {
				_, err := c.Do("PING")
				return err
			},
		}
	}

}
func (r *Redis)Process(conn net.Conn){
	buf := bufio.NewReader(conn)
	var ins string
	for {
		//读取协议
		protocol, err := ReadProtocol(buf)
		if err == io.EOF {
			println("client exit")
			//连接断开了
			return
		}
		println(string(protocol))
		if err != nil {
			_, e := conn.Write(r.ErrorRes(err))
			if e != nil {
				return
			}
			continue
		}
		cmdLine := r.DecodeProtocol(protocol)
		//解析连接
		if len(cmdLine)==1 && strings.ToUpper(cmdLine[0])=="COMMAND"{
			_,e :=conn.Write([]byte("+OK\r\n"))
			if e != nil {
				return
			}
			continue
		}
		//解析ping协议 获取要连接的实例
		if len(cmdLine)==2 && strings.ToUpper(cmdLine[0])=="PING"{
			ins = strings.ToLower(cmdLine[1])
		}
		if ins == "" {
			_, e := conn.Write(r.ErrorRes(errors.New("select an instance")))
			if e != nil {
				return
			}
			continue
		}
		pool ,err := r.GetPool(ins)
		if err != nil {
			_, e := conn.Write(r.ErrorRes(err))
			if e != nil {
				return
			}
			continue
		}
		resp,err := pool.Get().DoProtocol(protocol)
		fmt.Println("resp",resp,err)
		if err != nil {
			_, e := conn.Write(r.ErrorRes(err))
			if e != nil {
				return
			}
			continue
		}
		_, e := conn.Write(resp)
		if e != nil {
			return
		}
	}
}
func (r *Redis)DecodeProtocol(p []byte)[]string{
	if len(p)==0{
		return []string{}
	}
	cmd := bytes.Split(p,[]byte{'\r','\n'})
	cmd = cmd[1:len(cmd)-1]
	var cmdLine []string
	for i:=0;i<len(cmd);i+=2{
		cmdLine = append(cmdLine,string(cmd[i+1]))
	}
	return cmdLine
}

func readProtocolLine(buf *bufio.Reader) ([]byte, error) {
	p, err := buf.ReadBytes('\n')
	if err == bufio.ErrBufferFull {
		return nil,errors.New("long request line")
	}
	if err != nil {
		return nil, err
	}
	i := len(p) - 2
	if i < 0 || p[i] != '\r' {
		return nil, errors.New("bad request line terminator")
	}
	return p, nil
}
// parseLen parses bulk string and array lengths.
func parseProtocolLen(p []byte) (int, error) {
	if len(p) == 0 {
		return -1, errors.New("malformed length")
	}

	if p[0] == '-' && len(p) == 2 && p[1] == '1' {
		// handle $-1 and $-1 null replies.
		return -1, nil
	}

	var n int
	for _, b := range p {
		if b == '\r' || b == '\n' {
			continue
		}
		n *= 10
		if b < '0' || b > '9' {
			println("eeeeeeee")
			return -1, errors.New("illegal bytes in length")
		}
		n += int(b - '0')
	}

	return n, nil
}

// parseInt parses an integer reply.
func parseProtocolInt(p []byte) (int64, error) {
	if len(p) == 0 {
		return 0, errors.New("malformed integer")
	}

	var negate bool
	if p[0] == '-' {
		negate = true
		p = p[1:]
		if len(p) == 0 {
			return 0, errors.New("malformed integer")
		}
	}

	var n int64
	for _, b := range p {
		n *= 10
		if b < '0' || b > '9' {
			return 0, errors.New("illegal bytes in length")
		}
		n += int64(b - '0')
	}

	if negate {
		n = -n
	}
	return n, nil
}

func ReadProtocol(buf *bufio.Reader) ([]byte, error) {
	line, err := readProtocolLine(buf)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("short response line")
	}
	println(string(line))
	switch line[0] {
	case '$':
		n, err := parseProtocolLen(line[1:])
		if n < 0 || err != nil {
			return nil, err
		}
		p := make([]byte, n+2)
		_, err = io.ReadFull(buf, p)
		if err != nil {
			return nil, err
		}
		p = append(line, p...)
		return p, nil
	case '*':
		n, err := parseProtocolLen(line[1:])
		if n < 0 || err != nil {
			return nil, err
		}
		var r []byte

		for i := 0; i < n; i++ {
			tmp, err := ReadProtocol(buf)
			if err != nil {
				return nil, err
			}
			r = append(r, tmp...)
		}
		r = append(line, r...)
		return r, nil
	}
	return nil, errors.New("unexpected response line")
}
func (r *Redis)ErrorRes(err error)[]byte{
	return []byte("-"+fmt.Sprintf("%s",err)+"\r\n")
}