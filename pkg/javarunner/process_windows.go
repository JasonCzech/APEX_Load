//go:build windows

package javarunner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var managedProcessJobs sync.Map // map[*exec.Cmd]*managedJob

func supportsStrongProcessContainment() bool {
	return true
}

func startStrongProcessTree(cmd *exec.Cmd) error {
	return startProcessTree(cmd)
}

type managedJob struct {
	handle windows.Handle
	once   sync.Once
	err    error
}

func (j *managedJob) close() error {
	if j == nil {
		return nil
	}
	j.once.Do(func() {
		j.err = windows.CloseHandle(j.handle)
	})
	return j.err
}

func configureProcessTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func startProcessTree(cmd *exec.Cmd) error {
	configureProcessTree(cmd)
	handle, err := createKillOnCloseJob()
	if err != nil {
		return fmt.Errorf("create Java runner process job: %w", err)
	}
	job := &managedJob{handle: handle}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		_ = job.close()
		return err
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err == nil {
		err = windows.AssignProcessToJobObject(job.handle, process)
	}
	assigned := err == nil
	if process != 0 {
		_ = windows.CloseHandle(process)
	}
	if assigned {
		managedProcessJobs.Store(cmd, job)
		err = resumeProcessThreads(uint32(cmd.Process.Pid))
	}
	if err == nil {
		return nil
	}

	managedProcessJobs.Delete(cmd)
	jobCloseErr := job.close()
	processKillErr := cmd.Process.Kill()
	processKilled := processKillErr == nil || errors.Is(processKillErr, os.ErrProcessDone)

	// If either the assigned Job Object closed successfully or the still-
	// suspended, never-assigned root was killed directly, cmd.Wait cannot be
	// held open by target code and may reap synchronously. A resume failure is
	// different: some user code may already have run inside the assigned job,
	// so only a successful job close proves that every descendant was killed.
	containmentProven := (assigned && jobCloseErr == nil) || (!assigned && processKilled)
	if containmentProven {
		_ = cmd.Wait()
		return fmt.Errorf("isolate Java runner process tree: %w", err)
	}

	// Do not block the caller forever when containment cleanup was not proven.
	// ErrProcessContainmentFailed transfers the direct-root Wait exactly once to
	// ReapFailedStart so the owning runtime can track it through shutdown.
	failures := []error{
		ErrProcessContainmentFailed,
		fmt.Errorf("isolate Java runner process tree: %w", err),
	}
	if jobCloseErr != nil {
		failures = append(failures, fmt.Errorf("close Java runner process job: %w", jobCloseErr))
	}
	if !processKilled {
		failures = append(failures, fmt.Errorf("kill Java runner root process: %w", processKillErr))
	}
	return errors.Join(failures...)
}

func createKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var information windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func resumeProcessThreads(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	resumed := false
	for {
		if entry.OwnerProcessID == processID {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return openErr
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			if resumeErr != nil {
				return resumeErr
			}
			resumed = true
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		err = windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return err
		}
	}
	if !resumed {
		return fmt.Errorf("Java runner primary thread was unavailable")
	}
	return nil
}

func killProcessTree(cmd *exec.Cmd) error {
	if rawJob, ok := managedProcessJobs.LoadAndDelete(cmd); ok {
		job := rawJob.(*managedJob)
		// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE atomically terminates only the
		// processes owned by this exact job; unlike PID snapshots it cannot hit
		// an unrelated process after PID reuse.
		return job.close()
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return nil
	}
	// Non-managed probes retain a best-effort direct kill. Actual harness
	// execution always starts through StartProcessTree and therefore has a Job
	// Object ownership proof.
	err := cmd.Process.Kill()
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
