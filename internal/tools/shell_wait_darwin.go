package tools

import (
	"errors"

	"golang.org/x/sys/unix"
)

func waitShellExit(pid int) error {
	queue, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(queue)
	unix.CloseOnExec(queue)
	changes := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(queue, changes, events, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		// An already exited unreaped child may no longer accept a kqueue filter.
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if events[0].Flags&unix.EV_ERROR != 0 {
			err = unix.Errno(events[0].Data)
			if errors.Is(err, unix.ESRCH) {
				return nil
			}
			return err
		}
		return nil
	}
}
