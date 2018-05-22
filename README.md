Hammer 
======

Hammer is an interactive load tester.

Usage:

	hammer [flags] url
	flags:
		-q specify the initial QPS

Keybindings:

	- * multiply the current QPS by 2
	- 8 divide the current QPS by 2

Hammer sends a stream of requests to the given URL.

Display:

	QPS: 100
	Response histogram:
		OK: 11234 (X %)
		connection refused: 132 (Y %)

Should it use Go's http client or shell out to curl?
Maybe make that a flag.
