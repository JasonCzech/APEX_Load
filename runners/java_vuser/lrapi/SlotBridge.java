package lrapi;

import apexload.bridge.SlotRuntime;
import java.util.Map;

/** Loaded with the slot's private shim, never with a shared dependency. */
public final class SlotBridge implements SlotRuntime {
    public String eventAttributionContract() { return lr.__apexload_event_attribution_contract(); }
    public void bind(Map<String, String> context) { lr.__apexload_bind_slot(context); }
    public void beginInvocation(String id, Map<String, String> parameters) { lr.__apexload_begin_invocation(id, parameters); }
    public void endInvocation() { lr.__apexload_end_invocation(); }
    public ExitRequest exitRequest(Throwable thrown) {
        if (!(thrown instanceof lr.ExitSignal)) return null;
        lr.ExitSignal signal = (lr.ExitSignal) thrown;
        return new ExitRequest(signal.continuationOption(), signal.exitStatus());
    }
    public void handleExit(int option, int status) { lr.__apexload_handle_exit(option, status); }
    public boolean eventFallbackUsed() { return lr.__apexload_event_fallback_used(); }
    public boolean eventCaptureTruncated() { return lr.__apexload_event_capture_truncated(); }
}
