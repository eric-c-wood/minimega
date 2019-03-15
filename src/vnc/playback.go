// Copyright (2019) Sandia Corporation.
// Under the terms of Contract DE-AC04-94AL85000 with Sandia Corporation,
// the U.S. Government retains certain rights in this software.

package vnc

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/png"
	log "minilog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type playback struct {
	*Conn // embed

	ID    string // ID to identify playback
	rhost string // remote host

	start time.Time // start for when the playback started

	out         chan Event // events to write to vnc server
	signal      chan signal
	done        chan bool        // teardown playback
	screenshots chan *image.RGBA // screenshots of the VM

	sync.Mutex               // guards below
	depth      int           // how nested we are in LoadFiles
	duration   time.Duration // total playback duration
	e          string        // current event
	state      Control       // playback state, only Play or Pause
	closed     bool          // set after playback closed
	file       *os.File      // file that we are reading
	err        error         // error
}

type signal struct {
	kind Control
	data interface{}
}

// newPlayback creates a new playback with given id.
func newPlayback(id, rhost string) (*playback, error) {
	conn, err := Dial(rhost)
	if err != nil {
		return nil, err
	}

	return &playback{
		ID:          id,
		Conn:        conn,
		out:         make(chan Event),
		signal:      make(chan signal),
		done:        make(chan bool),
		screenshots: make(chan *image.RGBA),
		state:       Play,
	}, nil
}

func (p *playback) Closed() bool {
	p.Lock()
	defer p.Unlock()

	return p.closed
}

func (p *playback) Info() []string {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return nil
	}

	res := []string{
		p.ID,
		"playback kb",
	}

	if p.state == Pause {
		res = append(res, "PAUSED")
	} else {
		res = append(res, fmt.Sprintf("%v remaining", p.duration))
	}

	res = append(res, p.file.Name())

	return res
}

func (p *playback) Start(filename string) error {
	p.Lock()
	defer p.Unlock()

	err := (&SetEncodings{
		Encodings: []int32{CursorPseudoEncoding},
	}).Write(p.Conn)

	if err != nil {
		log.Error("unable to set encodings: %v", err)
		return err
	}

	p.start = time.Now()
	p.state = Play

	go p.writeEvents()
	go func() {
		if err := p.playFile(nil, filename); err != nil {
			log.Error("playback failed: %v", err)
		}

		// finished producing -- close output so the underlying connection
		// closes (see writeEvents)
		close(p.out)

		// finished with this playback
		p.Stop()
	}()
	go func() {
		// consume responses from the server
		for {
			msg, err := p.Conn.ReadMessage()
			if err != nil {
				log.Error("server to playback error: %v", err)
				break
			}

			switch msg := msg.(type) {
			case *FramebufferUpdate:
				for _, rect := range msg.Rectangles {
					// ignore non-image
					if rect.RGBA == nil {
						continue
					}

					select {
					case p.screenshots <- rect.RGBA:
						// success
					default:
						// drop
					}
				}
			case *SetColorMapEntries:
			case *Bell:
			}
		}
	}()

	return nil
}

func (p *playback) Step() error {
	p.Lock()
	defer p.Unlock()

	if p.state != Play || p.closed {
		return errors.New("playback not stepable")
	}

	p.signal <- signal{kind: Step}

	return nil
}

func (p *playback) Pause() error {
	p.Lock()
	defer p.Unlock()

	if p.state != Play || p.closed {
		return errors.New("playback not pauseable")
	}

	p.signal <- signal{kind: Pause}
	p.state = Pause

	return nil
}

func (p *playback) Continue() error {
	p.Lock()
	defer p.Unlock()

	if p.state != Pause || p.closed {
		return errors.New("playback not playable")
	}

	p.signal <- signal{kind: Play}
	p.state = Play

	return nil
}

func (p *playback) Stop() error {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return errors.New("playback has already stopped")
	}

	close(p.signal)
	p.closed = true

	return nil
}

func (p *playback) Inject(cmd string) error {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return errors.New("playback has already stopped")
	}

	e, err := parseEvent(cmd)
	if err != nil {
		return err
	}

	if event, ok := e.(Event); ok {
		p.out <- event
		return nil
	}

	switch e := e.(type) {
	case *LoadFileEvent:
		p.signal <- signal{kind: LoadFile, data: e}
	case *WaitForItEvent:
		p.signal <- signal{kind: WaitForIt, data: e}
	default:
		return fmt.Errorf("unknown event: %v", e)
	}

	return nil
}

func (p *playback) GetStep() (string, error) {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return "", errors.New("playback has already stopped")
	}

	return p.e, nil
}

func (v *playback) playFile(parent *os.File, filename string) error {
	if !filepath.IsAbs(filename) && parent != nil {
		// Our file is in the same directory as the parent
		filename = filepath.Join(filepath.Dir(parent.Name()), filename)
	}

	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// record that we're reading a new file and update the remaining duration
	v.addDuration(getDuration(f))

	old, err := v.setFile(f)
	if err != nil {
		return err
	}
	defer func() {
		v.resetFile(old)
	}()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		// Parse the event
		s := strings.SplitN(scanner.Text(), ":", 2)

		// Skip malformed commands and blank lines
		if len(s) != 2 {
			log.Debug("malformed vnc command: %s", scanner.Text())
			continue
		}

		// Ignore comments
		if strings.HasPrefix(s[0], "#") {
			log.Info("playback: %s", scanner.Text())
			continue
		}

		res, err := parseEvent(s[1])
		if err != nil {
			log.Error("invalid vnc message: `%s`", s[1])
			continue
		}

		// Set the current event context
		v.setStep(scanner.Text())

		duration, err := time.ParseDuration(s[0] + "ns")
		if err != nil {
			log.Errorln(err)
			continue
		}

		for {
			start := time.Now()

			select {
			case <-time.After(duration):
				v.addDuration(-duration)

				goto Event
			case sig, ok := <-v.signal:
				if !ok {
					// signal channel closed -- bail
					log.Info("abort playback of %v due to signal", f.Name())
					return nil
				}

				waited := start.Sub(time.Now())
				v.addDuration(-waited)

				// don't need to wait as long next time
				duration -= waited

				switch sig.kind {
				case Pause:
					sig, ok := <-v.signal
					if !ok {
						// signal channel closed -- bail
						log.Info("abort playback of %v due to signal", f.Name())
						return nil
					}

					switch sig.kind {
					case Play:
						// do nothing except keep playing
					default:
						log.Error("unexpected signal: %v", sig)
					}
				case LoadFile:
					e := sig.data.(LoadFileEvent)

					if err := v.playFile(f, e.File); err != nil {
						return err
					}
				case WaitForIt:
					e := sig.data.(*WaitForItEvent)

					if err := v.waitForIt(e); err != nil {
						return err
					}

				case Step:
					// decrease by the remaining
					v.addDuration(-duration)

					goto Event
				default:
					log.Error("unexpected signal: %v", sig)
				}
			}
		}

		// waited so process the event
	Event:
		switch e := res.(type) {
		case Event:
			v.out <- e
		case *LoadFileEvent:
			if err := v.playFile(f, e.File); err != nil {
				return err
			}
		case *WaitForItEvent:
			if err := v.waitForIt(e); err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *playback) waitForIt(e *WaitForItEvent) error {
	timeout := time.Duration(e.Timeout) * time.Nanosecond

	log.Info("playback %v, wait for %v, timeout = %v", p.ID, e.File, timeout)

	// TODO: load image

	fb := &FramebufferUpdateRequest{
		Width:  p.Conn.s.Width,
		Height: p.Conn.s.Height,
	}

	var i int
	for timeout > 0 {
		// request an updated screenshot
		if err := fb.Write(p.Conn); err != nil {
			return err
		}

		start := time.Now()

		select {
		case screenshot := <-p.screenshots:
			waited := time.Now().Sub(start)
			timeout -= waited

			log.Info("playback %v got screenshot after %v", p.ID, waited)

			// TODO: check for image
			f, err := os.Create(fmt.Sprintf("screenshot-%v.png", i))
			if err != nil {
				return fmt.Errorf("screenshot failed to write: %v", err)
			}

			i += 1

			if err := png.Encode(f, screenshot); err != nil {
				return fmt.Errorf("unable to encode screenshot: %v", err)
			}
			f.Close()
		case <-time.After(timeout):
			return fmt.Errorf("timeout waiting for %v", e.File)
		}

		// sleep and try again
		time.Sleep(time.Second)
		timeout -= time.Second
	}

	return fmt.Errorf("timeout waiting for %v", e.File)
}

func (p *playback) setFile(f *os.File) (old *os.File, err error) {
	p.Lock()
	defer p.Unlock()

	p.depth += 1
	if p.depth > 10 {
		log.Warn("recursive LoadFiles detected in vnc playback")
	}
	if p.depth > 100 {
		return nil, errors.New("too many recursive LoadFiles")
	}

	old, p.file = p.file, f
	return
}

func (p *playback) resetFile(old *os.File) {
	p.Lock()
	defer p.Unlock()

	p.depth -= 1
	p.file = old
}

func (p *playback) setStep(s string) {
	p.Lock()
	defer p.Unlock()

	p.e = s
}

func (p *playback) addDuration(d time.Duration) {
	p.Lock()
	defer p.Unlock()

	p.duration += d
}

// writeEvents reads events from the out channel and write them to the vnc
// connection. Closes the connection when it drains the channel.
func (p *playback) writeEvents() {
	defer p.Conn.Close()

	for e := range p.out {
		if err := e.Write(p.Conn); err != nil {
			log.Error("unable to write vnc event: %v", err)
			break
		}
	}

	// stop ourselves in a separate goroutine to avoid a deadlock
	go p.Stop()

	for range p.out {
		// drain the channel
	}
}
