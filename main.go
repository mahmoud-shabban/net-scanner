package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"sync"
	"time"
)

const (
	ssh   = "ssh"
	ping  = "ping"
	uname = "uname"
)

type Host struct {
	IP    netip.Addr
	Alive bool
	Uname string
}

func getHosts(host string) chan netip.Addr {
	ipChan := make(chan netip.Addr, 100)

	prefix, err := netip.ParsePrefix(host)
	if err != nil {
		log.Fatal(err)
	}

	ip := prefix.Addr()

	go func(ip netip.Addr) {
		defer close(ipChan)

		for i := 1; i < 255; i++ {
			ip = ip.Next()
			ipChan <- ip
		}

	}(ip)

	return ipChan

}

func testAlive(ctx context.Context, host netip.Addr) bool {
	cmd := exec.CommandContext(ctx, ping, "-c", "1", "-t", "2", host.String())

	if err := cmd.Run(); err != nil {
		return false
	}

	return true
}

func getUname(ctx context.Context, host netip.Addr, user string) string {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	config := fmt.Sprintf("%s@%s", user, host.String())
	cmd := exec.CommandContext(ctx, ssh, config, uname)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error: can't get uname for host %s, got this error (%w)\n", host.String(), err)
		return ""
	}

	return string(out)
}

func scanPrefix(ipChan chan netip.Addr) chan Host {
	hChan := make(chan Host, 100)

	go func() {
		defer close(hChan)
		wg := sync.WaitGroup{}
		limit := make(chan struct{}, 100)

		for host := range ipChan {
			wg.Add(1)
			limit <- struct{}{}

			go func(host netip.Addr) {
				defer wg.Done()
				defer func() { <-limit }()
				var h = Host{IP: host}

				ctx, cancel := context.WithTimeout(
					context.Background(),
					3*time.Second,
				)

				defer cancel()
				h.Alive = testAlive(ctx, host)
				hChan <- h
			}(host)
		}

		wg.Wait()

	}()

	return hChan
}

func unamePrefix(hchan chan Host, user string) chan Host {
	ch := make(chan Host, 1)

	go func() {
		defer close(ch)
		wg := sync.WaitGroup{}

		limit := make(chan struct{}, 100)

		for h := range hchan {
			if h.Alive {
				wg.Add(1)
				limit <- struct{}{}

				go func(h Host) {
					defer wg.Done()
					defer func() { <-limit }()

					ctx, cancel := context.WithTimeout(
						context.Background(),
						3*time.Second,
					)

					defer cancel()
					h.Uname = getUname(ctx, h.IP, user)
					ch <- h
				}(h)

			}

		}

		wg.Wait()
	}()

	return ch
}
func main() {
	var sub string

	// 192.168.0.133/30
	if len(os.Args) != 2 {
		log.Fatal("only one argument is permitted")
	}

	sub = os.Args[1]

	ipChan := getHosts(sub)

	hchan := scanPrefix(ipChan)

	user, err := user.Current()

	if err != nil {
		log.Fatal(err)
	}
	resultchan := unamePrefix(hchan, user.Username)

	for h := range resultchan {
		s, err := json.Marshal(h)

		if err != nil {
			log.Printf("%s host error marshling json %s\n", h.IP.String(), err.Error())
		}

		fmt.Println(string(s))
	}

}
