package notifier

import "testing"

func TestMessageOperationsSerializePerMessageOnly(t *testing.T) {
	var eventA, eventB tracked
	unlockA := eventA.lockMessage("chat")
	eventA.msgMu.Lock()
	lockA := eventA.messageOps["chat"]
	eventA.msgMu.Unlock()
	if lockA.TryLock() {
		lockA.Unlock()
		t.Fatal("the same message lock was not held")
	}
	unlockB := eventB.lockMessage("chat")
	unlockB()
	unlockA()
	if !lockA.TryLock() {
		t.Fatal("message lock stayed held after unlock")
	}
	lockA.Unlock()
}
