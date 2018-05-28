package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	termbox "github.com/nsf/termbox-go"
)

var numWorkers = flag.Int("w", 100, "number of concurrent workers")
var fetcher = flag.String("fetcher", "go", "type of fetcher to use: go|noop|curl")

func main() {
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

	go hammer(u)
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
	t0         time.Time
	t1         time.Time
	statusText string
}

// This chan is meant to have enough capacity for what can happen in a second.
var eventChan = make(chan event, 100000)

func hammer(url string) {
	ch := make(chan string)

	// Spin up workers.
	for i := 0; i < *numWorkers; i++ {
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
		switch *fetcher {
		case "curl":
			cmd := exec.Command("curl", "-s", "-S", u)
			out, _ := cmd.CombinedOutput()
			eventChan <- event{t0, time.Now(), string(out)}
		case "go":
			client := http.Client{Timeout: time.Duration(time.Second)}
			resp, err := client.Get(u)
			if resp != nil {
				// Read it, just in case that matters somehow.
				if _, err := ioutil.ReadAll(resp.Body); err != nil {
					eventChan <- event{t0, time.Now(), fmt.Sprintf("Failed to read response body: %v", err)}
					continue
				}
				if err := resp.Body.Close(); err != nil {
					eventChan <- event{t0, time.Now(), fmt.Sprintf("Failed to close response body: %v", err)}
					continue
				}
			}
			var st string
			if err != nil {
				parts := strings.Split(err.Error(), ": ")
				st = parts[len(parts)-1]
			} else {
				st = http.StatusText(resp.StatusCode)
			}
			eventChan <- event{t0, time.Now(), st}
		case "noop":
			eventChan <- event{t0, time.Now(), "Did nothing."}
		default:
			eventChan <- event{t0, time.Now(), fmt.Sprintf("Unrecognized value for --fetcher: %q\n", *fetcher)}
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

var interval = time.Second

func sendTermboxInterrupts() {
	for _ = range time.Tick(interval) {
		termbox.Interrupt()
	}
}

// draw repaints the termbox UI, showing stats.
func draw() {
	m := make(map[string]int)
loop:
	// Grab the latest events from the buffered event chan
	for {
		select {
		case e := <-eventChan:
			m[e.statusText]++
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
	tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, fmt.Sprintf("Target QPS: %d", atomic.LoadInt32(&reqQPS)))
	y++
	tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, fmt.Sprintf("%d workers", *numWorkers))
	y++
	y++
	if len(m) == 0 {
		tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, fmt.Sprintf("No responses in past %v", interval))
	} else {
		tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, fmt.Sprintf("Responses in past %v:", interval))
		y++
		for _, k := range keys {
			msg := fmt.Sprintf("  %s: %d", k, m[k])
			tbprint(0, y, termbox.ColorWhite, termbox.ColorBlack, msg)
			y++
		}
	}
	termbox.Flush()
}

func tbprint(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}
