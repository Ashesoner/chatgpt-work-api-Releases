//go:build windows

package processscope

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Scope struct {
	mu     sync.Mutex
	job    windows.Handle
	closed bool
}

func Attach(pid int) (*Scope, error) {
	if pid <= 0 {
		return nil, errors.New("PROCESS_SCOPE_PID_INVALID")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("PROCESS_JOB_CREATE_FAILED: %w", err)
	}
	keepJob := false
	defer func() {
		if !keepJob {
			_ = windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if result, setErr := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)),
	); result == 0 {
		if setErr == nil {
			setErr = errors.New("SetInformationJobObject returned zero")
		}
		return nil, fmt.Errorf("PROCESS_JOB_LIMITS_FAILED: %w", setErr)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("PROCESS_JOB_OPEN_FAILED: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return nil, fmt.Errorf("PROCESS_JOB_ASSIGN_FAILED: %w", err)
	}
	keepJob = true
	return &Scope{job: job}, nil
}

func (s *Scope) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.job == 0 {
		return nil
	}
	// Closing a kill-on-close job requests termination; it does not wait for
	// processes blocked in kernel filesystem work to release directory handles.
	// Keep the owned job queryable until it is empty, with a bounded wait.
	terminationErr := windows.TerminateJobObject(s.job, 1)
	if terminationErr == nil {
		deadline := time.Now().Add(3 * time.Second)
		for {
			var accounting struct {
				UserTime, KernelTime, PeriodUserTime, PeriodKernelTime           int64
				PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
			}
			queryErr := windows.QueryInformationJobObject(s.job, windows.JobObjectBasicAccountingInformation,
				uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
			if queryErr != nil {
				terminationErr = fmt.Errorf("PROCESS_JOB_QUERY_FAILED: %w", queryErr)
				break
			}
			if accounting.ActiveProcesses == 0 {
				break
			}
			if time.Now().After(deadline) {
				terminationErr = errors.New("PROCESS_JOB_TERMINATION_TIMEOUT")
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	err := windows.CloseHandle(s.job)
	s.job = 0
	if err != nil {
		return errors.Join(terminationErr, fmt.Errorf("PROCESS_JOB_CLOSE_FAILED: %w", err))
	}
	return terminationErr
}
