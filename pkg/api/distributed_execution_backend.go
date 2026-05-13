package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jasonczech/stormrunner/pkg/distributed/generatorrpc"
)

type generatorDial func(ctx context.Context, target string) (generatorClient, error)

type generatorClient interface {
	DistributeScript(ctx context.Context, script generatorrpc.Script) (generatorrpc.ScriptResult, error)
	AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error)
	AdjustVUsers(ctx context.Context, adjustment generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error)
	StopTest(ctx context.Context, testID string, graceful bool) (generatorrpc.StopResult, error)
	Close() error
}

type distributedExecutionBackend struct {
	targets []string
	timeout time.Duration
	dial    generatorDial
}

func newDistributedExecutionBackend(targets []string, timeout time.Duration, dial generatorDial) *distributedExecutionBackend {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if dial == nil {
		dial = func(ctx context.Context, target string) (generatorClient, error) {
			return generatorrpc.Dial(ctx, target)
		}
	}

	clonedTargets := append([]string(nil), targets...)
	return &distributedExecutionBackend{
		targets: clonedTargets,
		timeout: timeout,
		dial:    dial,
	}
}

func (b *distributedExecutionBackend) Run(tr *TestRunner, test *ManagedTest) {
	if test.ScriptShakeout {
		if err := tr.runScriptShakeout(test); err != nil {
			tr.failTestBeforeRun(test, err.Error())
			return
		}
	}
	if len(b.targets) == 0 {
		tr.failTestBeforeRun(test, "no generator targets configured")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	client, err := b.dial(ctx, b.targets[0])
	if err != nil {
		tr.failTestBeforeRun(test, fmt.Sprintf("connect generator: %v", err))
		return
	}
	defer client.Close()

	distributedScripts := map[string]struct{}{}
	for _, group := range test.Groups {
		if group.script == nil {
			tr.failTestBeforeRun(test, fmt.Sprintf("group %q has no compiled script", group.Name))
			return
		}

		scriptContent, contentHash, err := marshalDistributedScript(group.script)
		if err != nil {
			tr.failTestBeforeRun(test, fmt.Sprintf("serialize script %q: %v", group.ScriptName, err))
			return
		}

		if _, ok := distributedScripts[group.ScriptID]; !ok {
			if _, err := client.DistributeScript(ctx, generatorrpc.Script{
				ID:          group.ScriptID,
				Content:     scriptContent,
				ContentHash: contentHash,
			}); err != nil {
				tr.failTestBeforeRun(test, fmt.Sprintf("distribute script %q: %v", group.ScriptName, err))
				return
			}
			distributedScripts[group.ScriptID] = struct{}{}
		}

		assignment, err := client.AssignWork(ctx, generatorrpc.Assignment{
			TestID:        test.ID,
			ScriptID:      group.ScriptID,
			VUserCount:    int32(group.VUsers),
			RampUpSeconds: int32(group.rampUpDur / time.Second),
		})
		if err != nil {
			tr.failTestBeforeRun(test, fmt.Sprintf("assign work for group %q: %v", group.Name, err))
			return
		}
		if !assignment.Accepted {
			tr.failTestBeforeRun(test, fmt.Sprintf("generator rejected group %q: %s", group.Name, assignment.ErrorMessage))
			return
		}
	}

	tr.startDistributedTestRun(test, b)
}

func (b *distributedExecutionBackend) StopTest(tr *TestRunner, test *ManagedTest) error {
	if test.remoteTarget == "" {
		return fmt.Errorf("distributed generator target is not recorded")
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	client, err := b.dial(ctx, test.remoteTarget)
	if err != nil {
		return fmt.Errorf("connect generator: %w", err)
	}
	defer client.Close()

	stop, err := client.StopTest(ctx, test.ID, true)
	if err != nil {
		return fmt.Errorf("stop remote test: %w", err)
	}
	if !stop.Acknowledged {
		return fmt.Errorf("generator did not acknowledge stop for %s", test.ID)
	}
	return nil
}

func (b *distributedExecutionBackend) ScaleVUsers(tr *TestRunner, test *ManagedTest, groupName string, targetVUsers int) error {
	if len(test.Groups) != 1 {
		return fmt.Errorf("distributed execution does not support per-group scaling for multi-group tests yet")
	}
	if test.Groups[0].Name != groupName {
		return fmt.Errorf("test group not found: %s", groupName)
	}
	if test.remoteTarget == "" {
		return fmt.Errorf("distributed generator target is not recorded")
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	client, err := b.dial(ctx, test.remoteTarget)
	if err != nil {
		return fmt.Errorf("connect generator: %w", err)
	}
	defer client.Close()

	adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
		TestID:       test.ID,
		TargetVUsers: int32(targetVUsers),
	})
	if err != nil {
		return fmt.Errorf("adjust remote vusers: %w", err)
	}
	if !adjustment.Accepted {
		return fmt.Errorf("generator did not accept vuser adjustment for %s", test.ID)
	}
	return nil
}

func (b *distributedExecutionBackend) AdjustDuration(tr *TestRunner, test *ManagedTest, newDuration time.Duration) error {
	return fmt.Errorf("distributed execution does not support duration changes yet")
}

func (b *distributedExecutionBackend) PauseTest(tr *TestRunner, test *ManagedTest) error {
	return fmt.Errorf("distributed execution does not support pause yet")
}

func (b *distributedExecutionBackend) ResumeTest(tr *TestRunner, test *ManagedTest) error {
	return fmt.Errorf("distributed execution does not support resume yet")
}

func marshalDistributedScript(script any) ([]byte, string, error) {
	content, err := json.Marshal(script)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(content)
	return content, hex.EncodeToString(hash[:]), nil
}

func (tr *TestRunner) startDistributedTestRun(test *ManagedTest, backend testExecutionBackend) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()

	tr.mu.Lock()
	test.cancel = cancel
	test.multiEngine = nil
	test.executionBackend = backend
	test.remoteTarget = ""
	if distributed, ok := backend.(*distributedExecutionBackend); ok && len(distributed.targets) > 0 {
		test.remoteTarget = distributed.targets[0]
	}
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	tr.mu.Unlock()

	_ = ctx
	tr.broadcastEvent("test_started", test)
}