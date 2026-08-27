//go:build windows

package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

// Job object constants absent from the syscall package.
const (
	jobObjectExtendedLimitInformation = 9
	jobLimitKillOnJobClose            = 0x00002000
	// The rights AssignProcessToJobObject needs on the target process.
	processTerminate = 0x0001
	processSetQuota  = 0x0100
	// The tool is created suspended so it cannot spawn anything before it is
	// assigned to the job; only then is its thread resumed.
	createSuspended     = 0x00000004
	threadSuspendResume = 0x0002
	snapshotThreads     = 0x00000004
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject    = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJob  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject = kernel32.NewProc("TerminateJobObject")
	procThread32First      = kernel32.NewProc("Thread32First")
	procThread32Next       = kernel32.NewProc("Thread32Next")
	procOpenThread         = kernel32.NewProc("OpenThread")
	procResumeThread       = kernel32.NewProc("ResumeThread")
)

// basicLimitTailPad restores the trailing padding the C layout has: without
// it SetInformationJobObject is handed an undersized struct on windows/386.
const basicLimitTailPad = (8 - unsafe.Sizeof(jobBasicLimitInformation{})%8) % 8

// The layout SetInformationJobObject expects for
// JobObjectExtendedLimitInformation.
type (
	ioCounters struct {
		ReadOperationCount  uint64
		WriteOperationCount uint64
		OtherOperationCount uint64
		ReadTransferCount   uint64
		WriteTransferCount  uint64
		OtherTransferCount  uint64
	}
	jobBasicLimitInformation struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
	}
	jobExtendedLimitInformation struct {
		BasicLimitInformation jobBasicLimitInformation
		// C aligns the basic limits to 8 bytes for their int64 members, Go to
		// the word size, so on 32-bit builds the struct ends four bytes short
		// and every field after it lands at the wrong offset.
		_                     [basicLimitTailPad]byte
		IoInfo                ioCounters
		ProcessMemoryLimit    uintptr
		JobMemoryLimit        uintptr
		PeakProcessMemoryUsed uintptr
		PeakJobMemoryUsed     uintptr
	}
)

// threadEntry32 is the THREADENTRY32 record Thread32First and Thread32Next fill.
type threadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePri        int32
	DeltaPri       int32
	Flags          uint32
}

// processGroup is the tool and every process it spawns, held in a job object:
// Windows has no process group that survives the tool's own exit, so a job is
// the only handle that reaches the sub-agents an executor starts.
type processGroup struct {
	cmd *exec.Cmd
	job syscall.Handle
}

// newProcessGroup creates the job the tool is assigned to once it starts, and
// arranges for the tool to be created suspended so that assignment covers every
// descendant. A job that cannot be set up is reported before the tool starts:
// running without one would silently drop the guarantee that cancelling a run
// takes every sub-agent with it.
func newProcessGroup(cmd *exec.Cmd) (*processGroup, error) {
	handle, _, err := procCreateJobObject.Call(0, 0)
	if handle == 0 {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	job := syscall.Handle(handle)

	// Kill-on-close is what makes rrev's own death take the sub-agents with it:
	// the job dies with the last handle to it, which rrev holds.
	var limits jobExtendedLimitInformation
	limits.BasicLimitInformation.LimitFlags = jobLimitKillOnJobClose
	ok, _, err := procSetInformationJob.Call(uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		_ = syscall.CloseHandle(job)
		return nil, fmt.Errorf("configure job object: %w", err)
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
	return &processGroup{cmd: cmd, job: job}, nil
}

// started assigns the suspended tool to the job - which every process it spawns
// from then on inherits - and only then lets it run. Assignment that fails is
// reported rather than worked around: the tool would otherwise run outside the
// job, where cancelling the run cannot reach its descendants.
func (g *processGroup) started() error {
	if g.job == 0 {
		return errors.New("assign to job object: job object already released")
	}
	if g.cmd.Process == nil {
		return errors.New("assign to job object: process did not start")
	}
	pid := uint32(g.cmd.Process.Pid)
	if err := assignToJob(g.job, pid); err != nil {
		// The tool never joined the job, so terminating the job would report
		// success without touching it. Dropping the job makes kill reach the
		// still-suspended process instead; the caller stops the run there.
		g.close()
		return err
	}
	return resumeProcess(pid)
}

// assignToJob puts an already-created process into job.
func assignToJob(job syscall.Handle, pid uint32) error {
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, pid)
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = syscall.CloseHandle(process) }()
	if ok, _, err := procAssignProcessToJob.Call(uintptr(job), uintptr(process)); ok == 0 {
		return fmt.Errorf("assign process %d to job object: %w", pid, err)
	}
	return nil
}

// resumeProcess releases every thread of a process created suspended. A process
// that cannot be resumed would never run, so the failure is reported rather than
// left as a silent hang.
func resumeProcess(pid uint32) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(snapshotThreads, 0)
	if err != nil {
		return fmt.Errorf("snapshot threads of process %d: %w", pid, err)
	}
	defer func() { _ = syscall.CloseHandle(snapshot) }()

	var entry threadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	resumed := 0
	for next := procThread32First; ; next = procThread32Next {
		if ok, _, _ := next.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry))); ok == 0 {
			break
		}
		if entry.OwnerProcessID != pid {
			continue
		}
		if err := resumeThread(entry.ThreadID); err != nil {
			return err
		}
		resumed++
	}
	if resumed == 0 {
		return fmt.Errorf("resume process %d: no threads found", pid)
	}
	return nil
}

func resumeThread(tid uint32) error {
	handle, _, err := procOpenThread.Call(threadSuspendResume, 0, uintptr(tid))
	if handle == 0 {
		return fmt.Errorf("open thread %d: %w", tid, err)
	}
	defer func() { _ = syscall.CloseHandle(syscall.Handle(handle)) }()
	if count, _, err := procResumeThread.Call(handle); int32(count) < 0 {
		return fmt.Errorf("resume thread %d: %w", tid, err)
	}
	return nil
}

// kill terminates the whole job, falling back to the tool alone when there is
// no job to terminate.
func (g *processGroup) kill() {
	if g.job != 0 {
		if ok, _, _ := procTerminateJobObject.Call(uintptr(g.job), 1); ok != 0 {
			return
		}
	}
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
}

// close releases the job handle, which kills whatever the tool left running.
func (g *processGroup) close() {
	if g.job != 0 {
		_ = syscall.CloseHandle(g.job)
		g.job = 0
	}
}
