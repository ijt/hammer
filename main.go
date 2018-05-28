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
	numWorkers := flag.Int("w", 100, "number of concurrent workers")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Printf("Usage: hammer [flags] url\n")
		os.Exit(0)
	}
	u := flag.Arg(0)

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	termbox.SetInputMode(termbox.InputEsc | termbox.InputMouse)
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
	draw()

	go hammer(*numWorkers, u)
	go sendTermboxInterrupts()

	for {
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyArrowUp:
				q := atomic.LoadInt32(&reqQPS)
				atomic.StoreInt32(&reqQPS, 2*q)
				draw()
			case termbox.KeyArrowDown:
				q := atomic.LoadInt32(&reqQPS)
				atomic.StoreInt32(&reqQPS, q/2)
				draw()
			case termbox.KeyCtrlC:
				termbox.Close()
				os.Exit(0)
			}
		case termbox.EventInterrupt:
			draw()
		}
	}
}

var reqQPS int32 = 1

// An event
type event struct {
	t0   time.Time
	t1   time.Time
	err  error
	resp *http.Response
}

// This chan is meant to have enough capacity for what can happen in a second.
var eventChan = make(chan event, 100000)

func hammer(numWorkers int, url string) {
	ch := make(chan string)

	// Spin up workers.
	for i := 0; i < numWorkers; i++ {
		go worker(ch)
	}

	// Orchestrate the work.
	for _ = range ticktock() {
		ch <- url
	}
}

func worker(ch chan string) {
	for u := range ch {
		t0 := time.Now()
		client := http.Client{Timeout: time.Duration(time.Second)}
		resp, err := client.Get(u)
		eventChan <- event{t0, time.Now(), err, resp}
		if resp != nil {
			if err := resp.Body.Close(); err != nil {
				log.Printf("Failed to close response body: %v", err)
			}
		}
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

func sendTermboxInterrupts() {
	for _ = range time.Tick(time.Second) {
		termbox.Interrupt()
	}
}

func draw() {
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
	for _, k := range keys {
		msg := fmt.Sprintf("%s: %d", k, m[k])
		tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, msg)
		y++
	}
	termbox.Flush()
}

func tbprint(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}
