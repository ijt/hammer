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
	"sync"
	"sync/atomic"
	"time"

	termbox "github.com/nsf/termbox-go"
)

var numWorkers = flag.Int64("w", 100, "number of concurrent workers")
var fetcher = flag.String("fetcher", "go", "type of fetcher to use: go|noop|curl")

var interval = time.Second

var reqQPS int32 = 1

// This is a histogram of events over the past second.
var hmu sync.Mutex
var histogram = make(map[string]int)

func main() {
	flag.Parse()
	switch *fetcher {
	case "go":
	case "noop":
	case "curl":
	default:
		fmt.Printf("--fetcher set to %q, want one of \"go\", \"noop\", or \"curl\"\n", *fetcher)
		os.Exit(1)
	}

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

	doneChan := make(chan struct{})
	workChan := make(chan struct{}, 1000000)
	go hammer(u, workChan, doneChan)
	go sendTermboxInterrupts()

	for {
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyArrowUp:
				// Increase the target QPS.
				q := atomic.LoadInt32(&reqQPS)
				atomic.StoreInt32(&reqQPS, 2*q)
				draw()
			case termbox.KeyArrowDown:
				// Decrease the target QPS.
				q := atomic.LoadInt32(&reqQPS)
				atomic.StoreInt32(&reqQPS, q/2)
				draw()
			case termbox.KeyArrowRight:
				// Add some workers.
				for i := 0; i < 10; i++ {
					go worker(u, workChan, doneChan)
					atomic.StoreInt64(numWorkers, atomic.LoadInt64(numWorkers)+1)
				}
			case termbox.KeyArrowLeft:
				// Stop some existing workers.
				for i := 0; i < 10 && atomic.LoadInt64(numWorkers) > 0; i++ {
					doneChan <- struct{}{}
					atomic.StoreInt64(numWorkers, atomic.LoadInt64(numWorkers)-1)
				}
			case termbox.KeyCtrlC:
				// Quit
				termbox.Close()
				os.Exit(0)
			}
		case termbox.EventInterrupt:
			draw()
		}
	}
}

func hammer(url string, workChan, doneChan chan struct{}) {
	// Spin up workers.
	for i := int64(0); i < atomic.LoadInt64(numWorkers); i++ {
		go worker(url, workChan, doneChan)
	}

	// Feed the work channel reqQPS tickets per second.
	for _ = range time.Tick(time.Second) {
		// Drain workChan so we know it's starting from 0.
	loop:
		for {
			select {
			case <-workChan:
			default:
				break loop
			}
		}

		// Put QPS work tickets into workChan.
		for i := int32(0); i < atomic.LoadInt32(&reqQPS); i++ {
			workChan <- struct{}{}
		}
	}
}

func worker(url string, workChan chan struct{}, doneChan chan struct{}) {
	for {
		// Quit if the done chan says so.
		select {
		case <-doneChan:
			return
		default:
		}

		// Wait until there's work to do.
		<-workChan

		// Do some work.
		switch *fetcher {
		case "curl":
			cmd := exec.Command("curl", "-s", "-S", url)
			out, _ := cmd.CombinedOutput()
			addToHistogram(string(out))
		case "go":
			client := http.Client{Timeout: time.Duration(requestTimeout())}
			resp, err := client.Get(url)
			if resp != nil {
				// Read it, just in case that matters somehow.
				if _, err := ioutil.ReadAll(resp.Body); err != nil {
					addToHistogram(fmt.Sprintf("Failed to read response body: %v", err))
					continue
				}
				if err := resp.Body.Close(); err != nil {
					addToHistogram(fmt.Sprintf("Failed to close response body: %v", err))
					continue
				}
			}
			// status text
			var st string
			if err != nil {
				parts := strings.Split(err.Error(), ": ")
				st = parts[len(parts)-1]
			} else {
				st = http.StatusText(resp.StatusCode)
			}
			addToHistogram(st)
		case "noop":
			addToHistogram("Did nothing")
		default:
			addToHistogram(fmt.Sprintf("Unrecognized value for --fetcher: %q\n", *fetcher))
		}
	}
}

// addToHistogram increments the given string in the histogram and then
// decrements it again after a second.
func addToHistogram(s string) {
	hmu.Lock()
	defer hmu.Unlock()
	histogram[s]++
	go func() {
		<-time.After(time.Second)
		hmu.Lock()
		defer hmu.Unlock()
		histogram[s]--
		if histogram[s] == 0 {
			delete(histogram, s)
		}
	}()
}

// requestTimeout calculates how long workers should spend on each request.
func requestTimeout() time.Duration {
	d := time.Duration(float64(time.Second) * (float64(atomic.LoadInt64(numWorkers)) / float64(atomic.LoadInt32(&reqQPS))))
	if d > time.Second {
		d = time.Second
	}
	return d
}

func sendTermboxInterrupts() {
	for _ = range time.Tick(500 * time.Millisecond) {
		termbox.Interrupt()
	}
}

// draw repaints the termbox UI, showing stats.
func draw() {

	// Do the actual drawing.
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
	y := 0
	tbprint(0, y, termbox.ColorWhite, termbox.ColorDefault, fmt.Sprintf("Target QPS: %d", atomic.LoadInt32(&reqQPS)))
	y++
	tbprint(0, y, termbox.ColorWhite, termbox.ColorDefault, fmt.Sprintf("%d workers", atomic.LoadInt64(numWorkers)))
	y++
	tbprint(0, y, termbox.ColorWhite, termbox.ColorDefault, fmt.Sprintf("Request timeout: %v", requestTimeout()))
	y++
	y++
	hmu.Lock()
	defer hmu.Unlock()
	if len(histogram) == 0 {
		tbprint(0, y, termbox.ColorWhite, termbox.ColorDefault, fmt.Sprintf("No responses in past %v", interval))
	} else {
		tbprint(0, y, termbox.ColorWhite, termbox.ColorDefault, fmt.Sprintf("Responses in past %v:", interval))
		y++
		var keys []string
		for k := range histogram {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			msg := fmt.Sprintf("  %s: %d", k, histogram[k])
			tbprint(0, y, termbox.ColorWhite, termbox.ColorDefault, msg)
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
