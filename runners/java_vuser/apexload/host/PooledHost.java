package apexload.host;

import java.net.URL;
import java.net.URLClassLoader;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

/** One experimental execution/group/configuration partition in one JVM.
 * This is lifecycle machinery, not a sandbox. External containment, authenticated
 * transport, lease enforcement, and workload qualification remain mandatory. */
public final class PooledHost {
    private final Object lock = new Object();
    private final String generation;
    private final int capacity;
    private final URL shim;
    private final URL[] scripts;
    private final String target;
    private final URLClassLoader shared;
    private final Map<String,SlotWorker> slots = new LinkedHashMap<String,SlotWorker>();
    private final Set<String> closing = new HashSet<String>();
    private boolean committed, sealed, reapRequired, resourcesClosed;
    private String reapRequest = "";

    public PooledHost(String generation, int capacity, URL shim, URL[] scripts, URL[] dependencies, String target) {
        if (generation == null || !generation.matches("[A-Za-z0-9_-]{1,128}") || capacity < 1 || capacity > 256
                || shim == null || scripts == null || scripts.length == 0 || scripts.length > 256
                || dependencies == null || dependencies.length > 256 || target == null
                || !target.matches("[A-Za-z_$][A-Za-z0-9_.$]*")) throw new IllegalArgumentException("invalid host configuration");
        requireLocal(shim);
        for (URL url : scripts) requireLocal(url);
        for (URL url : dependencies) requireLocal(url);
        this.generation = generation; this.capacity = capacity; this.shim = shim;
        this.scripts = scripts.clone(); this.target = target;
        shared = new URLClassLoader(dependencies.clone(), PooledHost.class.getClassLoader());
    }

    private static void requireLocal(URL url) {
        if (url == null || !"file".equals(url.getProtocol()) || !url.getHost().isEmpty())
            throw new IllegalArgumentException("host artifacts must be approved local files");
    }

    public void prepareSlot(String expectedGeneration, String id, Map<String,String> context) {
        synchronized (lock) {
            checkGeneration(expectedGeneration);
            if (committed || sealed || slots.size() >= capacity || slots.containsKey(id))
                throw new IllegalStateException("host_admission_closed");
            SlotWorker worker = new SlotWorker(id, shim, scripts, shared, target, context);
            slots.put(id, worker);
        }
    }

    /** All slots must be prepared before commit. No constructor or lifecycle
     * runs here; the ramp controller explicitly submits each initial invocation. */
    public void commit(String expectedGeneration) {
        synchronized (lock) {
            checkGeneration(expectedGeneration);
            if (committed || sealed || slots.isEmpty()) throw new IllegalStateException("host_not_prepared");
            for (SlotWorker worker : slots.values()) worker.commit();
            committed = true;
        }
    }

    public CompletableFuture<SlotWorker.Result> invoke(String expectedGeneration, String slot, long sequence,
                                                       String request, String method, Map<String,String> parameters) {
        final CompletableFuture<SlotWorker.Result> result;
        synchronized (lock) {
            checkGeneration(expectedGeneration);
            SlotWorker worker = slots.get(slot);
            if (!committed || reapRequired || worker == null || closing.contains(slot))
                throw new IllegalStateException("slot_unavailable");
            result = worker.invoke(sequence, request, method, parameters);
        }
        // Callback registration can synchronously execute if invocation already
        // completed. Never register it under the host's control lock.
        return result.whenComplete((value, failure) -> {
            if (failure == null && "init".equals(method) && value.returnCode != 0) { requestReap(request); return; }
            if (failure != null) {
                boolean expectedClosure;
                synchronized (lock) { expectedClosure = closing.contains(slot) && !reapRequired; }
                if (!expectedClosure) requestReap(request);
            }
        });
    }

    public CompletableFuture<Void> closeSlot(String expectedGeneration, String slot, boolean qualifiedCleanup) {
        return closeSlot(expectedGeneration, slot, qualifiedCleanup, "");
    }

    public CompletableFuture<Void> closeSlot(String expectedGeneration, String slot, boolean qualifiedCleanup, String request) {
        SlotWorker worker;
        synchronized (lock) {
            checkGeneration(expectedGeneration);
            worker = slots.get(slot);
            if (worker == null) throw new IllegalArgumentException("unknown_slot");
            sealed = true;
            closing.add(slot);
        }
        if (!qualifiedCleanup) requestReap(request);
        worker.close();
        // Return a dependent future; the caller cannot complete the worker's
        // internal termination signal by cancelling/completing this view.
        return worker.stopped().whenComplete((ignored, failure) -> { if (failure != null) requestReap(request); }).thenApply(ignored -> null);
    }

    /** Uncertain termination/transport failure affects every peer. Preserve the
     * triggering result in the caller; the process owner reports pooled_host_reaped
     * for peers and kills containment without restarting any Java state. */
    public void requestReap() { requestReap(""); }

    private void requestReap(String triggeringRequest) {
        List<SlotWorker> victims;
        synchronized (lock) {
            if (reapRequired) return;
            reapRequired = true; sealed = true;
            reapRequest = triggeringRequest;
            closing.addAll(slots.keySet());
            victims = new ArrayList<SlotWorker>(slots.values());
        }
        for (SlotWorker worker : victims) worker.close();
    }

    public boolean isSealed() { synchronized (lock) { return sealed; } }
    public boolean requiresReap() { synchronized (lock) { return reapRequired; } }

    /** The first failing invocation is recorded before any peer is interrupted. */
    public String reapRequest() { synchronized (lock) { return reapRequest; } }

    /** Releases loader resources after all slot workers stopped. This proves
     * nothing about arbitrary background threads/native work; the caller must
     * still reap the host's process boundary and retain memory until it does. */
    public void awaitClosed(long timeoutMillis) throws Exception {
        if (timeoutMillis < 1 || timeoutMillis > 60000) throw new IllegalArgumentException("invalid close deadline");
        List<SlotWorker> workers;
        synchronized (lock) {
            if (!sealed || closing.size() != slots.size()) throw new IllegalStateException("host_has_live_slots");
            workers = new ArrayList<SlotWorker>(slots.values());
        }
        long deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis);
        for (SlotWorker worker : workers) {
            long remaining = deadline - System.nanoTime();
            if (remaining <= 0) throw new java.util.concurrent.TimeoutException("host_close_timeout");
            worker.stopped().get(remaining, TimeUnit.NANOSECONDS);
        }
        synchronized (lock) {
            if (!resourcesClosed) { shared.close(); resourcesClosed = true; }
        }
    }

    private void checkGeneration(String expected) {
        if (!generation.equals(expected)) throw new IllegalStateException("stale_host_generation");
    }
}
