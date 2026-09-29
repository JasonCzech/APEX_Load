package apexload.host;

import apexload.bridge.SlotRuntime;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.net.URL;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

/** Experimental host component, not an execution/containment boundary.
 * Preparation does not load user classes. The owner must enforce leases and
 * reap the entire host when cleanup is uncertain. A stopped worker alone does
 * not prove that application background work stopped. */
public final class SlotWorker {
    public enum State { PREPARED, COMMITTED, BUSY, CLOSING, CLOSED }
    public static final class Result {
        public final int returnCode;
        public final SlotRuntime.ExitRequest exit;
        public final boolean eventFallback, eventTruncated;
        Result(int code, SlotRuntime.ExitRequest exit, boolean fallback, boolean truncated) {
            this.returnCode = code; this.exit = exit;
            this.eventFallback = fallback; this.eventTruncated = truncated;
        }
    }
    private static final class Invocation {
        final long sequence;
        final String request, method;
        final Map<String,String> parameters;
        final CompletableFuture<Result> result = new CompletableFuture<Result>();
        Invocation(long sequence, String request, String method, Map<String,String> parameters) {
            this.sequence = sequence; this.request = request; this.method = method;
            this.parameters = Collections.unmodifiableMap(new HashMap<String,String>(parameters));
        }
    }

    private final Object lock = new Object();
    private final ArrayBlockingQueue<Invocation> queue = new ArrayBlockingQueue<Invocation>(1);
    private final CompletableFuture<Void> stopped = new CompletableFuture<Void>();
    private final URL shim;
    private final URL[] scripts;
    private final ClassLoader parent;
    private final String target;
    private final Map<String,String> context;
    private final Thread thread;
    private State state = State.PREPARED;
    private long lastSequence;
    private boolean initialized, ended;
    private Invocation current;

    public SlotWorker(String slotId, URL shim, URL[] scripts, ClassLoader parent,
                      String target, Map<String,String> context) {
        if (slotId == null || !slotId.matches("[A-Za-z0-9_-]{1,128}") || shim == null
                || scripts == null || scripts.length == 0 || parent == null
                || target == null || !target.matches("[A-Za-z_$][A-Za-z0-9_.$]*") || context == null)
            throw new IllegalArgumentException("invalid slot configuration");
        this.shim = shim; this.scripts = scripts.clone(); this.parent = parent; this.target = target;
        this.context = Collections.unmodifiableMap(new HashMap<String,String>(context));
        thread = new Thread(new Runnable() { public void run() { work(); } }, "apexload-slot-" + slotId);
        thread.setDaemon(true);
        thread.start();
    }

    public State state() { synchronized (lock) { return state; } }

    /** Commit only opens the invocation gate; it never constructs user code. */
    public void commit() {
        synchronized (lock) {
            if (state != State.PREPARED) throw new IllegalStateException("slot_not_prepared");
            state = State.COMMITTED;
        }
    }

    /** Exactly one outstanding invocation. Rejecting a second never releases
     * or interrupts the first. Sequence numbers are unique for the slot lifetime. */
    public CompletableFuture<Result> invoke(long sequence, String request, String method, Map<String,String> parameters) {
        if (sequence <= 0 || request == null || !request.matches("[A-Za-z0-9_-]{1,128}")
                || !("init".equals(method) || "action".equals(method) || "end".equals(method))
                || parameters == null || parameters.size() > 1024)
            throw new IllegalArgumentException("invalid invocation");
        synchronized (lock) {
            if (state != State.COMMITTED) throw new IllegalStateException("slot_unavailable");
            if (sequence <= lastSequence) throw new IllegalStateException("stale_request");
            if (ended || (!initialized && !"init".equals(method)) || (initialized && "init".equals(method)))
                throw new IllegalStateException("invalid_lifecycle");
            Invocation job = new Invocation(sequence, request, method, parameters);
            // Publish the BUSY state before the worker sees the request.
            lastSequence = sequence; current = job; state = State.BUSY;
            if (!queue.offer(job)) {
                // Invariant violation: permanently close, do not free busy capacity.
                state = State.CLOSING;
                thread.interrupt();
                throw new IllegalStateException("slot_queue_overflow");
            }
            return job.result;
        }
    }

    /** Independent control lane; cancellation permanently closes the slot.
     * The supervisor must reap the host if stopped() misses its grace deadline. */
    public void close() {
        synchronized (lock) {
            if (state == State.CLOSED || state == State.CLOSING) return;
            state = State.CLOSING;
            thread.interrupt();
        }
    }
    public CompletableFuture<Void> stopped() { return stopped; }

    private void work() {
        SlotLoader loader = null;
        SlotRuntime bridge = null;
        Object instance = null;
        Throwable failure = null;
        try {
            for (;;) {
                synchronized (lock) { if (state == State.CLOSING) break; }
                Invocation job;
                try { job = queue.poll(100, TimeUnit.MILLISECONDS); }
                catch (InterruptedException interrupted) {
                    synchronized (lock) { if (state == State.CLOSING) break; }
                    continue;
                }
                if (job == null) continue;
                boolean closing;
                synchronized (lock) { closing = state == State.CLOSING; }
                if (closing) {
                    job.result.completeExceptionally(new IllegalStateException("slot_closed"));
                    break;
                }
                Result result = null;
                Throwable invocationFailure = null;
                try {
                    if (loader == null) {
                        loader = new SlotLoader(shim, scripts, parent);
                        thread.setContextClassLoader(loader);
                        bridge = (SlotRuntime)loader.loadClass("lrapi.SlotBridge").getDeclaredConstructor().newInstance();
                        if (!"slot_thread_v1".equals(bridge.eventAttributionContract()))
                            throw new IllegalStateException("pooled_event_attribution_unsupported");
                        bridge.bind(context);
                    }
                    bridge.beginInvocation(job.request, job.parameters);
                    try {
                        if (instance == null) {
                            // initialize=true occurs only after commit+init submission,
                            // with lr context and invocation parameters already bound.
                            Class<?> type = Class.forName(target, false, loader);
                            if (type.getClassLoader() != loader) throw new IllegalStateException("target_not_slot_private");
                            Class.forName(target, true, loader);
                            instance = type.getDeclaredConstructor().newInstance();
                        }
                        Method method = instance.getClass().getMethod(job.method);
                        if (method.getReturnType() != Integer.TYPE && method.getReturnType() != Void.TYPE)
                            throw new IllegalStateException("target_return_type_invalid");
                        Object value = method.invoke(instance);
                        result = new Result(value == null ? 0 : ((Integer)value).intValue(), null, false, false);
                    } catch (InvocationTargetException wrapped) {
                        Throwable cause = wrapped.getCause();
                        SlotRuntime.ExitRequest exit = bridge.exitRequest(cause);
                        if (exit == null || !"action".equals(job.method) || (exit.option != 1 && exit.option != 2)) throw cause;
                        bridge.handleExit(exit.option, exit.status);
                        result = new Result(0, exit, false, false);
                    } finally { bridge.endInvocation(); }
                    result = new Result(result.returnCode, result.exit, bridge.eventFallbackUsed(), bridge.eventCaptureTruncated());
                } catch (Throwable thrown) { invocationFailure = thrown; }
                synchronized (lock) {
                    current = null;
                    if (invocationFailure != null || ("init".equals(job.method) && result.returnCode != 0)) state = State.CLOSING;
                    else {
                        if ("init".equals(job.method)) initialized = true;
                        if ("end".equals(job.method)) ended = true;
                        if (state != State.CLOSING) state = State.COMMITTED;
                    }
                }
                if (invocationFailure != null) job.result.completeExceptionally(invocationFailure);
                else job.result.complete(result);
            }
        } catch (Throwable thrown) { failure = thrown; }
        finally {
            thread.setContextClassLoader(parent);
            if (loader != null) try { loader.close(); } catch (Throwable thrown) { if (failure == null) failure = thrown; }
            Invocation abandoned;
            synchronized (lock) {
                state = State.CLOSED;
                abandoned = current;
                current = null;
                queue.clear();
            }
            if (abandoned != null && !abandoned.result.isDone())
                abandoned.result.completeExceptionally(new IllegalStateException("slot_closed"));
            if (failure == null) stopped.complete(null); else stopped.completeExceptionally(failure);
        }
    }
}
