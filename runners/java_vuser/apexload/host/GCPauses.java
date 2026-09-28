package apexload.host;

import java.lang.reflect.Method;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;

/** One bounded host-level pause stream. Reflection keeps shared source Java 8
 * compatible; only runtimes exposing the built-in JFR pause event can collect.
 * No user classes, System.gc(), transaction clocks, or slot callbacks are used. */
public final class GCPauses implements AutoCloseable {
    private final ArrayDeque<String> events = new ArrayDeque<String>();
    private String status = "unsupported";
    private long sequence, evicted;
    private Object stream;
    private Method close;

    public static GCPauses start() {
        final GCPauses capture = new GCPauses();
        try {
            Class<?> recorder = Class.forName("jdk.jfr.FlightRecorder");
            Class<?> type = Class.forName("jdk.jfr.EventType");
            Object instance = recorder.getMethod("getFlightRecorder").invoke(null);
            long found = -1;
            for (Object event : (List<?>)recorder.getMethod("getEventTypes").invoke(instance)) {
                if ("jdk.GCPhasePause".equals(type.getMethod("getName").invoke(event))) {
                    found = (Long)type.getMethod("getId").invoke(event);
                    break;
                }
            }
            if (found < 0) return capture;
            final long eventID = found;
            Class<?> recording = Class.forName("jdk.jfr.consumer.RecordingStream");
            Class<?> recorded = Class.forName("jdk.jfr.consumer.RecordedEvent");
            final Method eventType = recorded.getMethod("getEventType");
            final Method typeID = type.getMethod("getId");
            final Method start = recorded.getMethod("getStartTime");
            final Method duration = recorded.getMethod("getDuration");
            capture.stream = recording.getConstructor().newInstance();
            capture.close = recording.getMethod("close");
            recording.getMethod("setMaxSize", long.class).invoke(capture.stream, 8L << 20);
            recording.getMethod("setMaxAge", Duration.class).invoke(capture.stream, Duration.ofSeconds(30));
            Map<String,String> settings = new HashMap<String,String>();
            settings.put(eventID + "#enabled", "true");
            settings.put(eventID + "#threshold", "0 ns");
            settings.put(eventID + "#stackTrace", "false");
            recording.getMethod("setSettings", Map.class).invoke(capture.stream, settings);
            recording.getMethod("onEvent", String.class, Consumer.class).invoke(capture.stream,
                "jdk.GCPhasePause", (Consumer<Object>) event -> {
                    try {
                        // A script-defined event with the same name is not a GC pause.
                        if ((Long)typeID.invoke(eventType.invoke(event)) != eventID) return;
                        Instant when = (Instant)start.invoke(event);
                        long nanos = Math.addExact(Math.multiplyExact(when.getEpochSecond(), 1000000000L), when.getNano());
                        capture.record(nanos, ((Duration)duration.invoke(event)).toNanos());
                    } catch (Throwable failure) { capture.failed(); }
                });
            recording.getMethod("onError", Consumer.class).invoke(capture.stream,
                (Consumer<Throwable>) failure -> capture.failed());
            recording.getMethod("onClose", Runnable.class).invoke(capture.stream,
                (Runnable) () -> capture.failed());
            capture.status = "available";
            recording.getMethod("startAsync").invoke(capture.stream);
        } catch (ClassNotFoundException unsupported) {
            capture.close();
        } catch (Throwable failure) {
            capture.failed();
            capture.close();
        }
        return capture;
    }

    // Package-private deterministic fixture seam; production accepts only the
    // platform event selected before any user code is loaded.
    synchronized void record(long start, long duration) {
        if (start <= 0 || duration < 0 || duration > Long.MAX_VALUE - start || sequence == Long.MAX_VALUE) {
            failed(); return;
        }
        if (events.size() == 128) { events.removeFirst(); evicted++; }
        events.addLast(++sequence + "," + start + "," + duration);
    }

    synchronized void fields(Map<String,String> target) {
        StringBuilder body = new StringBuilder();
        for (String event : events) body.append(event).append('\n');
        target.put("gc_pause_status", status);
        target.put("gc_pause_events", body.toString());
        target.put("gc_pause_evicted", Long.toString(evicted));
    }
    private synchronized void failed() { status = "failed"; }
    public void close() {
        if (stream != null && close != null) {
            try { close.invoke(stream); } catch (Throwable failure) { failed(); }
        }
    }
}
