package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	termbox "github.com/nsf/termbox-go"
)

func main() {
	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()

	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Printf("Usage: hammer [flags] url\n")
		os.Exit(0)
	}
	u := flag.Arg(0)

	go hammer(u)
	go report()

	termbox.SetInputMode(termbox.InputEsc | termbox.InputMouse)
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
loop:
	for {
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyArrowUp:
				q := atomic.LoadInt32(&reqQPS)
				atomic.StoreInt32(&reqQPS, 2*q)
			case termbox.KeyArrowDown:
				q := atomic.LoadInt32(&reqQPS)
				atomic.StoreInt32(&reqQPS, q/2)
			case termbox.KeyCtrlC:
				break loop
			}
		}
	}
}

var reqQPS int32 = 1

// Number of pending requests
var pending int32 = 0

// An event
type event struct {
	t0   time.Time
	t1   time.Time
	err  error
	resp *http.Response
}

// This chan is meant to have enough capacity for what can happen in a second.
var eventChan = make(chan event, 100000)

func hammer(u string) {
	for _ = range ticktock() {
		go strike(u)
	}
}

func ticktock() chan struct{} {
	c := make(chan struct{})
	go func() {
		for {
			time.Sleep(time.Duration(int32(time.Second) / atomic.LoadInt32(&reqQPS)))
			c <- struct{}{}
		}
	}()
	return c
}

func strike(u string) {
	atomic.AddInt32(&pending, 1)
	defer atomic.AddInt32(&pending, -1)
	t0 := time.Now()
	resp, err := http.Get(u)
	eventChan <- event{t0, time.Now(), err, resp}
	if resp != nil {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}
}

func report() {
	for _ = range time.Tick(time.Second) {
		m := make(map[string]int)
	loop:
		// Grab the latest events from the buffered event chan
		for {
			select {
			case e := <-eventChan:
				s := ""
				switch {
				case e.err != nil:
					s = e.err.Error()
					parts := strings.Split(s, ": ")
					s = parts[len(parts)-1]
				default:
					s = http.StatusText(e.resp.StatusCode)
				}
				m[s]++
			default:
				break loop
			}
		}
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
		y := 0
		tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, fmt.Sprintf("QPS: %d", atomic.LoadInt32(&reqQPS)))
		y++
		tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, fmt.Sprintf("pending: %d", atomic.LoadInt32(&pending)))
		y++
		for _, k := range keys {
			msg := fmt.Sprintf("%s: %d", k, m[k])
			tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, msg)
			y++
		}
		termbox.Flush()
	}
}

func tbprint(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}
