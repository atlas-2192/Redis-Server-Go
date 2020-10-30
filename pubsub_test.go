package redcon

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPubSub(t *testing.T) {
	addr := ":12346"
	done := make(chan bool)
	go func() {
		var ps PubSub
		go func() {
			tch := time.NewTicker(time.Millisecond * 5)
			defer tch.Stop()
			channels := []string{"achan1", "bchan2", "cchan3", "dchan4"}
			for i := 0; ; i++ {
				select {
				case <-tch.C:
				case <-done:
					for {
						var empty bool
						ps.mu.Lock()
						if len(ps.conns) == 0 {
							if ps.chans.Len() != 0 {
								panic("chans not empty")
							}
							empty = true
						}
						ps.mu.Unlock()
						if empty {
							break
						}
						time.Sleep(time.Millisecond * 10)
					}
					done <- true
					return
				}
				channel := channels[i%len(channels)]
				message := fmt.Sprintf("message %d", i)
				ps.Publish(channel, message)
			}
		}()
		t.Fatal(ListenAndServe(addr, func(conn Conn, cmd Command) {
			switch strings.ToLower(string(cmd.Args[0])) {
			default:
				conn.WriteError("ERR unknown command '" +
					string(cmd.Args[0]) + "'")
			case "publish":
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for '" +
						string(cmd.Args[0]) + "' command")
					return
				}
				count := ps.Publish(string(cmd.Args[1]), string(cmd.Args[2]))
				conn.WriteInt(count)
			case "subscribe", "psubscribe":
				if len(cmd.Args) < 2 {
					conn.WriteError("ERR wrong number of arguments for '" +
						string(cmd.Args[0]) + "' command")
					return
				}
				command := strings.ToLower(string(cmd.Args[0]))
				for i := 1; i < len(cmd.Args); i++ {
					if command == "psubscribe" {
						ps.Psubscribe(conn, string(cmd.Args[i]))
					} else {
						ps.Subscribe(conn, string(cmd.Args[i]))
					}
				}
			}
		}, nil, nil))
	}()

	final := make(chan bool)
	go func() {
		select {
		case <-time.Tick(time.Second * 30):
			panic("timeout")
		case <-final:
			return
		}
	}()

	// create 10 connections
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func(i int) {
			defer wg.Done()
			var conn net.Conn
			for i := 0; i < 5; i++ {
				var err error
				conn, err = net.Dial("tcp", addr)
				if err != nil {
					time.Sleep(time.Second / 10)
					continue
				}
			}
			if conn == nil {
				panic("could not connect to server")
			}
			defer conn.Close()

			regs := make(map[string]int)
			var maxp int
			var maxs int
			fmt.Fprintf(conn, "subscribe achan1\r\n")
			fmt.Fprintf(conn, "subscribe bchan2 cchan3\r\n")
			fmt.Fprintf(conn, "psubscribe a*1\r\n")
			fmt.Fprintf(conn, "psubscribe b*2 c*3\r\n")

			// collect 50 messages from each channel
			rd := bufio.NewReader(conn)
			var buf []byte
			for {
				line, err := rd.ReadBytes('\n')
				if err != nil {
					panic(err)
				}
				buf = append(buf, line...)
				n, resp := ReadNextRESP(buf)
				if n == 0 {
					continue
				}
				buf = nil
				if resp.Type != Array {
					panic("expected array")
				}
				var vals []RESP
				resp.ForEach(func(item RESP) bool {
					vals = append(vals, item)
					return true
				})

				name := string(vals[0].Data)
				switch name {
				case "subscribe":
					if len(vals) != 3 {
						panic("invalid count")
					}
					ch := string(vals[1].Data)
					regs[ch] = 0
					maxs, _ = strconv.Atoi(string(vals[2].Data))
				case "psubscribe":
					if len(vals) != 3 {
						panic("invalid count")
					}
					ch := string(vals[1].Data)
					regs[ch] = 0
					maxp, _ = strconv.Atoi(string(vals[2].Data))
				case "message":
					if len(vals) != 3 {
						panic("invalid count")
					}
					ch := string(vals[1].Data)
					regs[ch] = regs[ch] + 1
				case "pmessage":
					if len(vals) != 4 {
						panic("invalid count")
					}
					ch := string(vals[1].Data)
					regs[ch] = regs[ch] + 1
				}
				if len(regs) == 6 && maxp == 3 && maxs == 3 {
					ready := true
					for _, count := range regs {
						if count < 50 {
							ready = false
							break
						}
					}
					if ready {
						// all messages have been received
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	// notify sender
	done <- true
	// wait for sender
	<-done
	// stop the timeout
	final <- true
}
