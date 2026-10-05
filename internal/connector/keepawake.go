package connector

import (
	"log"
	"time"

	"github.com/odmrs/ember/internal/device"
)

// Awake is whether the linked Device is being kept awake.
type Awake string

const (
	AwakeOff Awake = "off"
	AwakeOn  Awake = "on"
	// AwakeUnavailable means the Device could not run the keep-awake command.
	AwakeUnavailable Awake = "unavailable"
)

// keepAwakeCommand stops the Device's screen saver, and with it the sleep
// that drops Wi-Fi, for as long as its stdin stays open. When stdin closes,
// because ember closed it or because the connection is gone, the screen saver
// is allowed again. If the first lipc-set-prop fails (127 when the Device
// does not have it), the command ends at once with that status. It is a
// fixed string: nothing from the Profile or the user goes into it.
const keepAwakeCommand = "lipc-set-prop com.lab126.powerd preventScreenSaver 1 || exit $?; " +
	"cat >/dev/null; " +
	"lipc-set-prop com.lab126.powerd preventScreenSaver 0"

// releaseWait is how long a release waits for the Device to allow the screen
// saver again before the link goes on without it.
const releaseWait = 3 * time.Second

// keeper runs the keep-awake command on one link. Only the goroutine holding
// the link uses it.
type keeper struct {
	c *Connector
	d *device.Device
	p *device.Process // nil when not running
}

// set starts or stops the command and returns the resulting state.
func (k *keeper) set(on bool) Awake {
	if !on {
		k.stop()
		k.c.mu.Lock()
		k.c.awakeFailed = false
		k.c.mu.Unlock()
		return AwakeOff
	}
	if k.p != nil {
		return AwakeOn
	}
	k.c.mu.Lock()
	failed := k.c.awakeFailed
	k.c.mu.Unlock()
	if failed {
		return AwakeUnavailable
	}
	p, err := k.d.Start(keepAwakeCommand)
	if err != nil {
		if k.fail() {
			log.Printf("connector: keep-awake unavailable: the command could not start: %v", err)
		}
		return AwakeUnavailable
	}
	k.p = p
	return AwakeOn
}

// done is closed when the running command has ended; nil when none runs.
func (k *keeper) done() <-chan struct{} {
	if k.p == nil {
		return nil
	}
	return k.p.Done()
}

// fail records that keep-awake cannot work on this Device and reports
// whether that is news, so it is logged once.
func (k *keeper) fail() bool {
	k.c.mu.Lock()
	defer k.c.mu.Unlock()
	first := !k.c.awakeFailed
	k.c.awakeFailed = true
	return first
}

// ended handles the command ending while ember still wanted it. Without an
// exit status the connection is going down and the link ends with it. With
// status 0 the command ran to its end: something on the Device closed its
// input, and it allowed the screen saver again on the way out. Nothing
// failed, so keep-awake shows off for this link and starts again on the
// next one. Any other status means the Device could not keep itself awake,
// which trying again will not change: it is logged once and left alone
// until keep-awake is switched off and on.
func (k *keeper) ended() Awake {
	status, ok := k.p.ExitStatus()
	k.p.Close()
	k.p = nil
	if !ok {
		return AwakeOff
	}
	if status == 0 {
		log.Printf("connector: keep-awake ended on the Device's side; it starts again with the next link")
		return AwakeOff
	}
	if k.fail() {
		if status == 127 {
			log.Printf("connector: keep-awake unavailable: the Device has no lipc-set-prop")
		} else {
			log.Printf("connector: keep-awake unavailable: lipc-set-prop exited with status %d", status)
		}
	}
	return AwakeUnavailable
}

// stop closes the command's stdin and waits for the Device to allow the
// screen saver again.
func (k *keeper) stop() {
	if k.p == nil {
		return
	}
	_ = k.p.CloseStdin()
	select {
	case <-k.p.Done():
	case <-time.After(releaseWait):
		log.Printf("connector: keep-awake did not end within %v of closing its input", releaseWait)
	}
	k.p.Close()
	k.p = nil
}
