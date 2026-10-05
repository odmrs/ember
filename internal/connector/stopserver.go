package connector

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/odmrs/ember/internal/device"
)

// dropbearPIDFile is where KOReader's SSH plugin starts dropbear writing its
// pid, and what the plugin's menu reads to show the server as running.
const dropbearPIDFile = "/tmp/dropbear_koreader.pid"

// stopWait is how long the Device gets to start the stop command before the
// link is closed anyway.
const stopWait = 3 * time.Second

// ErrStopFailed means the link was closed but the Device's SSH server could
// not be told to stop.
var ErrStopFailed = errors.New("connector: could not stop the SSH server on the Device")

// stopServerCommand does what KOReader's SSH plugin does when its server is
// stopped from the menu: TERM the listening dropbear (only that process; each
// connection, ours included, is served by a child of it), delete the two
// firewall rules the plugin added for the port when it started, and remove
// the pid file once the listener is gone, so the menu shows the server off.
//
// Deleting the OUTPUT rule can cut the packets of this very connection, so
// the command detaches from it and waits a second before acting: the link is
// closed by then. port is an int formatted with %d; nothing else varies.
func stopServerCommand(port int) string {
	script := fmt.Sprintf("sleep 1; "+
		`pid=$(cat %[2]s); kill -TERM "$pid"; sleep 1; `+
		"iptables -D INPUT -p tcp --dport %[1]d -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT; "+
		"iptables -D OUTPUT -p tcp --sport %[1]d -m conntrack --ctstate ESTABLISHED -j ACCEPT; "+
		`[ -d "/proc/$pid" ] || rm -f %[2]s`, port, dropbearPIDFile)
	return "nohup sh -c '" + script + "' >/dev/null 2>&1 &"
}

// stopServer starts the stop command on d. The shell returns as soon as the
// command is in the background; if the Device does not answer within
// stopWait the link is closed anyway, which also ends the wait.
func stopServer(d *device.Device, port int) error {
	done := make(chan error, 1)
	go func() {
		out, err := d.Exec(stopServerCommand(port))
		if err != nil && len(out) > 0 {
			err = fmt.Errorf("%w: %s", err, out)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			log.Printf("connector: stopping the SSH server on the Device: %v", err)
			return fmt.Errorf("%w: %v", ErrStopFailed, err)
		}
		return nil
	case <-time.After(stopWait):
		log.Printf("connector: the Device did not confirm the SSH server stop within %v", stopWait)
		return fmt.Errorf("%w: no answer within %v", ErrStopFailed, stopWait)
	}
}
