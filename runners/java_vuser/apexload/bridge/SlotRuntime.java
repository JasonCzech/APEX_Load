package apexload.bridge;

import java.util.Map;

/** Parent-loaded API. No lrapi or application types cross this boundary. */
public interface SlotRuntime {
    String eventAttributionContract();
    void bind(Map<String, String> context);
    void beginInvocation(String requestId, Map<String, String> parameters);
    void endInvocation();
    ExitRequest exitRequest(Throwable thrown);
    void handleExit(int option, int status);
    boolean eventFallbackUsed();
    boolean eventCaptureTruncated();

    final class ExitRequest {
        public final int option;
        public final int status;
        public ExitRequest(int option, int status) {
            this.option = option;
            this.status = status;
        }
    }
}
