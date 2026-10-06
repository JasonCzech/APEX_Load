package lrapi;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.nio.file.StandardOpenOption;
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.Deque;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentLinkedDeque;
import java.util.concurrent.atomic.AtomicLong;

public final class lr {
	private static volatile Map<String, String> slotContext = Collections.emptyMap();
	private static volatile boolean slotBound;

	// Only the pooled host calls this, before loading any user class. Dedicated
	// execution retains the existing process environment/property behavior.
	public static synchronized void __apexload_bind_slot(Map<String, String> context) {
		if (slotBound || context == null) throw new IllegalStateException("slot context already bound or missing");
		Map<String, String> values = new HashMap<String, String>();
		for (Map.Entry<String, String> entry : context.entrySet()) {
			String name = entry.getKey();
			if (!("APEXLOAD_VUSER_ID".equals(name) || "APEXLOAD_GROUP_NAME".equals(name)
				|| "APEXLOAD_SCENARIO_ID".equals(name) || "APEXLOAD_IGNORE_THINK_TIME".equals(name)
				|| "APEXLOAD_THINK_TIME_LIMIT_MS".equals(name) || "APEXLOAD_JAVA_VUSER_EVENT_FILE".equals(name))
				|| entry.getValue() == null) throw new IllegalArgumentException("unsupported slot setting");
			values.put(name, entry.getValue());
		}
		slotContext = Collections.unmodifiableMap(values);
		slotBound = true;
	}

	private static String setting(String name) {
		return slotBound ? slotContext.get(name) : System.getenv(name);
	}
    public static final int PASS = 0;
    public static final int FAIL = 1;
    public static final int AUTO = 2;
    public static final int STOP = 3;
    // Published LoadRunner continuation constants are compile-time inlined into
    // precompiled Actions classes; these numeric values are therefore part of
    // the binary compatibility contract.
    public static final int EXIT_VUSER = 0;
    public static final int EXIT_ACTION_AND_CONTINUE = 1;
    public static final int EXIT_ITERATION_AND_CONTINUE = 2;
    public static final int EXIT_VUSER_AFTER_ITERATION = 3;
    public static final int EXIT_VUSER_AFTER_ACTION = 4;
    public static final int EXIT_MAIN_ITERATION_AND_CONTINUE = 5;

    private static final Map<String, String> parameters = new ConcurrentHashMap<>();
    // The script's parameter braces (.usr ParamLeftBrace/ParamRightBrace). The
    // engine delivers non-default braces as these reserved lr_parameters keys.
    static final String PARAM_LEFT_BRACE_KEY = "__apexload_param_left_brace";
    static final String PARAM_RIGHT_BRACE_KEY = "__apexload_param_right_brace";
    private static volatile String paramLeftBrace = "{";
    private static volatile String paramRightBrace = "}";
    private static final Map<String, Long> timers = new ConcurrentHashMap<>();
    // Explicit per-transaction status overrides set via set_transaction /
    // set_transaction_status. An entry here wins over LR_AUTO error inference.
    // Each open transaction owns its status. Keeping state by identity avoids
    // coupling nested transaction bookkeeping to lookup mechanics; LoadRunner
    // requires names to remain unique while they are open.
    private static final Deque<TransactionState> openTransactions = new ConcurrentLinkedDeque<>();
    // Monotonic count of errors raised via lr.error_message during this run.
    private static final AtomicLong errorCount = new AtomicLong();
    private static final AtomicLong eventSequence = new AtomicLong();
    private static final AtomicLong transactionSequence = new AtomicLong();
    private static final String EVENT_CONTRACT = "apexload.java_vuser.lrapi_events.v2";
    private static final long MAX_EVENT_FILE_BYTES = 1024L * 1024L;
    private static final int MAX_EVENT_TYPE_CHARS = 1024;
    private static final int MAX_EVENT_NAME_CHARS = 16 * 1024;
    private static final int MAX_EVENT_MESSAGE_CHARS = 64 * 1024;
    private static final int MAX_TRANSACTION_NAME_CHARS = 256;
    // Go protocol.Result durations are time.Duration nanoseconds. Keep manual
    // transaction timestamps within that exact representable range.
    private static final long MAX_TRANSACTION_DURATION_MS = Long.MAX_VALUE / 1_000_000L;
    private static volatile String currentRequestId = "";
    private static volatile Thread slotInvocationThread;
    private static volatile boolean slotInvocationActive;
    private static volatile boolean slotAttributionFailed;
    private static volatile boolean eventFallbackUsed;
    private static volatile boolean eventCaptureTruncated;

    public static String __apexload_event_attribution_contract() { return "slot_thread_v1"; }

    private lr() {
    }

    public static void __apexload_seed_parameters(Map<String, String> values) {
        if (values == null) {
            return;
        }
        for (Map.Entry<String, String> entry : values.entrySet()) {
            __apexload_set_parameter(entry.getKey(), entry.getValue());
        }
    }

    // Begins one init/action/end invocation in a persistent Vuser JVM. Saved
    // parameters intentionally survive; timers, transaction bookkeeping and
    // LR_AUTO error state are invocation-local and must not leak to the next
    // iteration.
    public static void __apexload_begin_invocation(String requestId, Map<String, String> values) {
        if (slotBound) {
            if (slotAttributionFailed) throw new IllegalStateException("pooled_event_attribution_failed");
            slotInvocationThread = Thread.currentThread();
            slotInvocationActive = true;
        }
        timers.clear();
        openTransactions.clear();
        errorCount.set(0L);
        eventSequence.set(0L);
        transactionSequence.set(0L);
        eventFallbackUsed = false;
        eventCaptureTruncated = false;
        currentRequestId = requestId == null ? "" : requestId.trim();
        __apexload_seed_parameters(values);
    }

    // Closes malformed/open transactions at the invocation boundary. Emitting
    // an explicit failed terminal event prevents an incomplete Java action from
    // being reported as a successful business transaction.
    public static void __apexload_end_invocation() {
        try {
            TransactionState transaction;
            while ((transaction = openTransactions.poll()) != null) {
                long endedAt = System.currentTimeMillis();
                emitTransactionAt(
                        endedAt,
                        "transaction_unmatched",
                        transaction.name,
                        Integer.toString(FAIL),
                        transaction.id,
                        transaction.parentId,
                        transactionRawDurationMillis(transaction, endedAt),
                        transaction.thinkTimeMillis,
                        transaction.wastedTimeMillis);
            }
            if (slotBound && slotAttributionFailed) throw new IllegalStateException("pooled_event_attribution_failed");
        } finally {
            if (slotBound) slotInvocationActive = false;
        }
    }

    // lr.exit ignores any transaction it interrupts. Drop those transactions
    // without fabricating pass/fail children, but retain a structured diagnostic
    // event so the governed run remains reviewable.
    public static void __apexload_handle_exit(int continuationOption, int exitStatus) {
        TransactionState transaction;
        while ((transaction = openTransactions.poll()) != null) {
            emitTransaction(
                    "transaction_ignored",
                    transaction.name,
                    continuationOption + " " + exitStatus,
                    transaction.id,
                    transaction.parentId);
        }
    }

    public static boolean __apexload_event_fallback_used() {
        return eventFallbackUsed;
    }

    public static boolean __apexload_event_capture_truncated() {
        return eventCaptureTruncated;
    }

    public static void __apexload_set_parameter(String name, String value) {
        if (name == null || name.trim().isEmpty()) {
            return;
        }
        String trimmed = name.trim();
        if (PARAM_LEFT_BRACE_KEY.equals(trimmed) || PARAM_RIGHT_BRACE_KEY.equals(trimmed)) {
            String brace = value == null ? "" : value.trim();
            if (!brace.isEmpty() && brace.length() <= 8) {
                if (PARAM_LEFT_BRACE_KEY.equals(trimmed)) {
                    paramLeftBrace = brace;
                } else {
                    paramRightBrace = brace;
                }
            }
            return;
        }
        parameters.put(trimmed, value == null ? "" : value);
    }

    public static String __apexload_get_parameter(String name) {
        if (name == null) {
            return "";
        }
        return parameters.getOrDefault(name.trim(), "");
    }

    public static int abort(Object... args) {
        emit("runtime", "abort", join(args));
        throw new RuntimeException("lr.abort");
    }

    public static int advance_param(Object... args) {
        emit("parameter", valueAt(args, 0), "advance");
        return PASS;
    }

    public static String aws_get_secret(Object... args) {
        return externalSecret("aws_get_secret", args);
    }

    public static String cyberark_get_vault(Object... args) {
        return externalSecret("cyberark_get_vault", args);
    }

    public static int debug_message(Object... args) {
        emit("debug", valueAt(args, 0), join(args));
        return PASS;
    }

    // Exact OpenText lrapi descriptor (ILjava/lang/String;)I, so Actions
    // classes compiled against the real lrapi jar link without
    // NoSuchMethodError. The Object... overload serves source-level calls.
    public static int debug_message(int messageLevel, String message) {
        emit("debug", Integer.toString(messageLevel), message == null ? "" : message);
        return PASS;
    }

    public static String decrypt(Object... args) {
        emit("review", "decrypt", "Machine-key-bound ciphertext was passed through by the sidecar shim.");
        return valueAt(args, 0);
    }

    public static String decrypt_ex(Object... args) {
        return decrypt(args);
    }

    public static Object deserialize(Object... args) {
        emit("review", "deserialize", "Java object deserialization is not implemented by the sidecar shim.");
        return null;
    }

    public static int enable_redirection(Object... args) {
        emit("runtime", "enable_redirection", join(args));
        return PASS;
    }

    public static String encrypt_ex(Object... args) {
        emit("review", "encrypt_ex", "Encryption helper was passed through by the sidecar shim.");
        return valueAt(args, 0);
    }

    public static int end_sub_trans(Object... args) {
        return end_sub_transaction(args);
    }

    public static int end_sub_transaction(Object... args) {
        return endSubTransaction(valueAt(args, 0), intAt(args, 1, AUTO));
    }

    // Keep the published lrapi.lr descriptor for precompiled LoadRunner
    // Actions classes. Object... overloads remain only as a source-compatibility
    // fallback for scripts that call the shim with dynamically typed values.
    public static int end_sub_transaction(String name, int status) {
        return endSubTransaction(name, status);
    }

    public static int end_sub_trans(String name, int status) {
        return endSubTransaction(name, status);
    }

    private static int endSubTransaction(String name, int requestedStatus) {
        if (requestedStatus != AUTO && !isConcreteTransactionStatus(requestedStatus)) {
            emit("error", name, "lr.end_sub_transaction rejected an invalid status");
            return -1;
        }
        TransactionState transaction = currentTransaction(name, true);
        if (transaction == null) {
            warnUnmatchedEnd("end_sub_transaction", name);
            return -1;
        }
        int status = resolveTransactionStatus(transaction, requestedStatus);
        long endedAt = System.currentTimeMillis();
        emitTransactionAt(
                endedAt,
                "transaction_end",
                name,
                Integer.toString(status),
                transaction.id,
                transaction.parentId,
                transactionRawDurationMillis(transaction, endedAt),
                transaction.thinkTimeMillis,
                transaction.wastedTimeMillis);
        openTransactions.remove(transaction);
        // The API return reports technical success, not the recorded business
        // transaction status.
        return PASS;
    }

    public static double end_timer(Object... args) {
        String name = valueAt(args, 0);
        long now = System.currentTimeMillis();
        Long started = timers.remove(name);
        double elapsed = started == null ? 0.0 : (now - started) / 1000.0;
        emit("timer_end", name, Double.toString(elapsed));
        return elapsed;
    }

    public static int end_transaction(Object... args) {
        return endTransaction(valueAt(args, 0), intAt(args, 1, AUTO));
    }

    public static int end_transaction(String name, int status) {
        return endTransaction(name, status);
    }

    private static int endTransaction(String name, int requestedStatus) {
        if (requestedStatus != AUTO && !isConcreteTransactionStatus(requestedStatus)) {
            emit("error", name, "lr.end_transaction rejected an invalid status");
            return -1;
        }
        TransactionState transaction = currentTransaction(name, false);
        if (transaction == null) {
            warnUnmatchedEnd("end_transaction", name);
            return -1;
        }
        int status = resolveTransactionStatus(transaction, requestedStatus);
        long endedAt = System.currentTimeMillis();
        emitTransactionAt(
                endedAt,
                "transaction_end",
                name,
                Integer.toString(status),
                transaction.id,
                transaction.parentId,
                transactionRawDurationMillis(transaction, endedAt),
                transaction.thinkTimeMillis,
                transaction.wastedTimeMillis);
        openTransactions.remove(transaction);
        return PASS;
    }

    public static int error_message(Object... args) {
        return error_message(join(args));
    }

    public static int error_message(String message) {
        if (message == null) {
            return -1;
        }
        errorCount.incrementAndGet();
        // OpenText makes this conditional on the runtime setting named "Fail
        // open transactions on lr.error_message". When enabled it applies to
        // all currently open LR_AUTO transactions.
        if (boolFromEnv("APEXLOAD_FAIL_OPEN_TRANSACTIONS_ON_ERROR_MESSAGE")) {
            for (TransactionState transaction : openTransactions) {
                transaction.status = FAIL;
            }
        }
        emit("error", "", message);
        return message.length();
    }

    public static byte[] eval_data(Object... args) {
        return eval_string(args).getBytes(StandardCharsets.UTF_8);
    }

    public static int eval_int(Object... args) {
        return eval_int(valueAt(args, 0));
    }

    public static int eval_int(String name) {
        try {
            return Integer.parseInt(eval_string(name).trim());
        } catch (NumberFormatException ex) {
            return 0;
        }
    }

    // Substitutes parameter references in one pass, as LoadRunner does: a
    // substituted value is not scanned again, and text between braces that
    // names no parameter stays literal. A reference resolves by exact name,
    // then by the importer's canonical name (lowercase, other characters as
    // "_"), because imported .prm parameters arrive under canonical names while
    // the Java source keeps the recorded spelling.
    public static String eval_string(String value) {
        if (value == null) {
            return "";
        }
        String left = paramLeftBrace;
        String right = paramRightBrace;
        StringBuilder out = null;
        int index = 0;
        int scan = 0;
        while (true) {
            int start = value.indexOf(left, scan);
            if (start < 0) {
                break;
            }
            int nameStart = start + left.length();
            int end = value.indexOf(right, nameStart);
            if (end < 0) {
                break;
            }
            String resolved = end - nameStart <= 128 ? resolveParameter(value.substring(nameStart, end)) : null;
            if (resolved == null) {
                scan = nameStart;
                continue;
            }
            if (out == null) {
                out = new StringBuilder(value.length());
            }
            out.append(value, index, start).append(resolved);
            index = end + right.length();
            scan = index;
        }
        if (out == null) {
            return value;
        }
        return out.append(value, index, value.length()).toString();
    }

    private static String resolveParameter(String name) {
        if (name.isEmpty()) {
            return null;
        }
        String exact = parameters.get(name);
        if (exact != null) {
            return exact;
        }
        // Only an identifier can name a parameter; "/ban" in "</ban>" must not
        // resolve to "ban" when the delimiters are angle brackets.
        if (!isParameterIdentifier(name)) {
            return null;
        }
        String canonical = canonicalParameterName(name);
        if (canonical.isEmpty()) {
            return null;
        }
        for (Map.Entry<String, String> entry : parameters.entrySet()) {
            if (canonical.equals(canonicalParameterName(entry.getKey()))) {
                return entry.getValue();
            }
        }
        return null;
    }

    private static boolean isParameterIdentifier(String name) {
        for (int i = 0; i < name.length(); i++) {
            char c = name.charAt(i);
            boolean letter = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_';
            if (!letter && (i == 0 || c < '0' || c > '9')) {
                return false;
            }
        }
        return true;
    }

    // Mirrors canonicalSourceParameterName in the Go importer.
    static String canonicalParameterName(String name) {
        StringBuilder out = new StringBuilder(name.length());
        boolean underscore = true;
        for (char c : name.trim().toLowerCase(java.util.Locale.ROOT).toCharArray()) {
            boolean keep = (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9');
            if (keep) {
                out.append(c);
                underscore = false;
            } else if (!underscore) {
                out.append('_');
                underscore = true;
            }
        }
        int length = out.length();
        if (length > 0 && out.charAt(length - 1) == '_') {
            out.setLength(length - 1);
        }
        return out.toString();
    }

    public static String eval_string(Object... args) {
        return eval_string(valueAt(args, 0));
    }

    public static int exit(Object... args) {
        int continuationOption = intAt(args, 0, EXIT_VUSER);
        int exitStatus = intAt(args, 1, FAIL);
        exit(continuationOption, exitStatus);
        return PASS; // unreachable; retained for the legacy varargs descriptor.
    }

    // Official binary descriptor: (II)V. Harness recognizes ExitSignal so an
    // Actions method is unwound immediately instead of continuing backend work
    // after lr.exit. Full engine continuation semantics are capability-gated
    // separately; see the Java support matrix.
    public static void exit(int continuationOption, int exitStatus) {
        emit("runtime", "exit", continuationOption + " " + exitStatus);
        throw new ExitSignal(continuationOption, exitStatus);
    }

    public static double get_attrib_double(Object... args) {
        return doubleAt(args, 1, 0.0);
    }

    public static long get_attrib_long(Object... args) {
        return (long) doubleAt(args, 1, 0.0);
    }

    public static String get_attrib_string(Object... args) {
        return valueAt(args, 1);
    }

    public static int get_debug_message(Object... args) {
        return 0;
    }

    public static String get_group_name(Object... args) {
        return defaultString(setting("APEXLOAD_GROUP_NAME"), "java_vuser");
    }

    public static String get_host_name(Object... args) {
        return defaultString(System.getenv("HOSTNAME"), "localhost");
    }

    public static String get_master_host_name(Object... args) {
        return defaultString(System.getenv("APEXLOAD_CONTROLLER_HOST"), "controller");
    }

    public static Object get_object(Object... args) {
        return null;
    }

    public static String get_scenario_id(Object... args) {
        return defaultString(setting("APEXLOAD_SCENARIO_ID"), "scenario");
    }

    public static double get_transaction_duration(Object... args) {
        return get_transaction_duration(valueAt(args, 0));
    }

    public static double get_transaction_duration(String name) {
        TransactionState transaction = currentTransaction(name);
        if (transaction == null) {
            return 0.0;
        }
        return elapsedMillis(transaction.startedAtMillis, System.currentTimeMillis()) / 1000.0;
    }

    public static int get_transaction_status(Object... args) {
        return get_transaction_status(valueAt(args, 0));
    }

    public static int get_transaction_status(String name) {
        TransactionState transaction = currentTransaction(name);
        return transaction == null ? -1 : transaction.status;
    }

    public static double get_transaction_think_time(Object... args) {
        return get_transaction_think_time(valueAt(args, 0));
    }

    public static double get_transaction_think_time(String name) {
        TransactionState transaction = currentTransaction(name);
        return transaction == null ? 0.0 : transaction.thinkTimeMillis / 1000.0;
    }

    public static double get_transaction_wasted_time(Object... args) {
        return get_transaction_wasted_time(valueAt(args, 0));
    }

    public static double get_transaction_wasted_time(String name) {
        TransactionState transaction = currentTransaction(name);
        return transaction == null ? 0.0 : transaction.wastedTimeMillis / 1000.0;
    }

    public static int get_vuser_id(Object... args) {
        return intFromEnv("APEXLOAD_VUSER_ID", 0);
    }

    public static int log(Object... args) {
        emit("log", "", join(args));
        return PASS;
    }

    public static int log_message(Object... args) {
        emit("log", "", join(args));
        return PASS;
    }

    // Exact OpenText lrapi descriptor (Ljava/lang/String;)I.
    public static int log_message(String message) {
        emit("log", "", message == null ? "" : message);
        return PASS;
    }

    public static int message(Object... args) {
        emit("log", "", join(args));
        return PASS;
    }

    // Exact OpenText lrapi descriptor (Ljava/lang/String;)I.
    public static int message(String message) {
        emit("log", "", message == null ? "" : message);
        return PASS;
    }

    public static int next_row(Object... args) {
        emit("parameter", valueAt(args, 0), "next_row");
        return PASS;
    }

    public static int output_message(Object... args) {
        emit("log", "", join(args));
        return PASS;
    }

    // Exact OpenText lrapi descriptor (Ljava/lang/String;)I.
    public static int output_message(String message) {
        emit("log", "", message == null ? "" : message);
        return PASS;
    }

    public static int peek_events(Object... args) {
        emit("runtime", "peek_events", "");
        return PASS;
    }

    public static String read_file(Object... args) {
        return readTextFile(valueAt(args, 0));
    }

    public static String read_xml(Object... args) {
        return read_file(args);
    }

    public static int redirect(Object... args) {
        emit("runtime", "redirect", join(args));
        return PASS;
    }

    public static int rendezvous(Object... args) {
        emit("rendezvous", valueAt(args, 0), "");
        return PASS;
    }

    public static int save_data(Object... args) {
        if (args != null && args.length >= 2) {
            String name = valueAt(args, 1);
            if (!name.isEmpty()) {
                Object value = args[0];
                if (value instanceof byte[]) {
                    parameters.put(name, Base64.getEncoder().encodeToString((byte[]) value));
                } else {
                    parameters.put(name, valueAt(args, 0));
                }
            }
        }
        return PASS;
    }

    public static int save_int(Object... args) {
        return save_int(intAt(args, 0, 0), valueAt(args, 1));
    }

    public static int save_int(int value, String paramName) {
        if (paramName == null || paramName.trim().isEmpty()) {
            return FAIL;
        }
        parameters.put(paramName.trim(), Integer.toString(value));
        return PASS;
    }

    public static int save_string(Object... args) {
        return save_string(valueAt(args, 0), valueAt(args, 1));
    }

    public static int save_string(String value, String paramName) {
        if (paramName == null || paramName.trim().isEmpty()) {
            return FAIL;
        }
        parameters.put(paramName.trim(), value == null ? "" : value);
        return PASS;
    }

    public static int set_debug_message(Object... args) {
        emit("debug", "set_debug_message", join(args));
        return PASS;
    }

    public static int set_transaction(Object... args) {
        return set_transaction(valueAt(args, 0), doubleAt(args, 1, Double.NaN), intAt(args, 2, AUTO));
    }

    public static int set_transaction(String name, double duration, int status) {
        long durationMillis = transactionDurationMillis(duration);
        if (!validTransactionName(name) || durationMillis < 0L || !isConcreteTransactionStatus(status)) {
            emit("error", name, "lr.set_transaction rejected an invalid duration or status");
            return FAIL;
        }
        long endedAt = System.currentTimeMillis();
        long startedAt = safeSubtract(endedAt, durationMillis);
        String transactionId = (currentRequestId.isEmpty() ? "legacy" : currentRequestId)
                + "-tx-" + transactionSequence.incrementAndGet();
        emitTransactionAt(startedAt, "transaction_start", name, "manual", transactionId, "", -1L);
        emitTransactionAt(
                endedAt,
                "transaction_end",
                name,
                Integer.toString(status),
                transactionId,
                "",
                durationMillis,
                0L,
                0L);
        // Like end_transaction, the return value reports API success, not the
        // supplied business status.
        return PASS;
    }

    public static int set_transaction_status(Object... args) {
        return set_transaction_status(intAt(args, 0, AUTO));
    }

    public static int set_transaction_status(int status) {
        if (!isConcreteTransactionStatus(status)) {
            return -1;
        }
        // lr_set_transaction_status sets the status of the open transaction(s).
        // Record it as an explicit override so a subsequent end_transaction with
        // LR_AUTO honors it instead of inferring pass/fail from error events.
        for (TransactionState transaction : openTransactions) {
            transaction.status = status;
        }
        emit("transaction_status", "", Integer.toString(status));
        return PASS;
    }

    public static int sleep(Object... args) {
        return sleepMillis((long) doubleAt(args, 0, 0.0));
    }

    public static int start_sub_transaction(Object... args) {
        return start_sub_transaction(valueAt(args, 0), valueAt(args, 1));
    }

    public static int start_sub_transaction(String name, String parentName) {
        TransactionState parent = currentTransaction(parentName, false);
        if (parent == null) {
            warnMissingSubTransactionParent(name, parentName);
            return -1;
        }
        TransactionState transaction = openTransaction(name, parent.id, true);
        if (transaction == null) {
            return -1;
        }
        emitTransaction("transaction_start", name, "sub", transaction.id, transaction.parentId);
        return PASS;
    }

    public static int start_timer(Object... args) {
        String name = valueAt(args, 0);
        timers.put(name, System.currentTimeMillis());
        emit("timer_start", name, "");
        return PASS;
    }

    public static int start_transaction(Object... args) {
        return start_transaction(valueAt(args, 0));
    }

    public static int start_transaction(String name) {
        TransactionState parent = openTransactions.peek();
        TransactionState transaction = openTransaction(name, parent == null ? "" : parent.id, false);
        if (transaction == null) {
            return -1;
        }
        emitTransaction("transaction_start", name, "", transaction.id, transaction.parentId);
        return PASS;
    }

    // Official binary descriptor: (D)V.
    public static void think_time(double seconds) {
        long millis = durationMillis(seconds);
        millis = effectiveThinkTimeMillis(millis);
        addThinkTimeToOpenTransactions(millis);
        emit("think_time", "", Long.toString(millis));
        // The whole Vuser class runs inside a single java_vuser_run protocol
        // action, so the engine cannot re-insert pacing between lr.think_time
        // calls. The pause has to happen here in the JVM or it is lost entirely.
        if (millis > 0L) {
            sleepMillis(millis);
        }
    }

    public static int think_time(Object... args) {
        think_time(doubleAt(args, 0, 0.0));
        return PASS;
    }

    public static String unmask(Object... args) {
        emit("review", "unmask", "Masked value was passed through by the sidecar shim.");
        return valueAt(args, 0);
    }

    public static int user_data_point(Object... args) {
        emit("metric", valueAt(args, 0), valueAt(args, 1));
        return PASS;
    }

    public static int value_check(Object... args) {
        emit("review", "value_check", join(args));
        return PASS;
    }

    public static int vuser_status_message(Object... args) {
        emit("status", "", join(args));
        return PASS;
    }

    // Exact OpenText lrapi descriptor (Ljava/lang/String;)I.
    public static int vuser_status_message(String message) {
        emit("status", "", message == null ? "" : message);
        return PASS;
    }

    public static int wasted_time(Object... args) {
        addWastedTime(intAt(args, 0, 0));
        return PASS;
    }

    // Official Java descriptor: (I)I.
    public static int wasted_time(int wasteTime) {
        addWastedTime(wasteTime);
        return PASS;
    }

    public static void setParam(String name, String value) {
        save_string(value, name);
    }

    public static String getParam(String name) {
        return parameters.getOrDefault(name, "");
    }

    private static String externalSecret(String source, Object[] args) {
        String name = valueAt(args, 0);
        emit("external_secret", source, name);
        return parameters.getOrDefault(name, "");
    }

    private static int sleepMillis(long millis) {
        try {
            Thread.sleep(Math.max(0L, millis));
            return PASS;
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            return FAIL;
        }
    }

    private static String readTextFile(String path) {
        if (path == null || path.trim().isEmpty()) {
            return "";
        }
        try {
            return new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8);
        } catch (Exception ex) {
            emit("error", "read_file", ex.toString());
            return "";
        }
    }

    private static String valueAt(Object[] args, int index) {
        if (args == null || index < 0 || index >= args.length || args[index] == null) {
            return "";
        }
        Object value = args[index];
        if (value instanceof byte[]) {
            return new String((byte[]) value, StandardCharsets.UTF_8);
        }
        return eval_string(String.valueOf(value));
    }

    private static int intAt(Object[] args, int index, int fallback) {
        if (args == null || index < 0 || index >= args.length || args[index] == null) {
            return fallback;
        }
        Object value = args[index];
        if (value instanceof Number) {
            return ((Number) value).intValue();
        }
        try {
            return Integer.parseInt(String.valueOf(value).trim());
        } catch (NumberFormatException ex) {
            return fallback;
        }
    }

    private static synchronized TransactionState openTransaction(
            String name,
            String parentId,
            boolean subTransaction) {
        String normalized = name == null ? "" : name;
        if (!validTransactionName(normalized) || currentTransaction(normalized) != null) {
            emit("error", normalized,
                    "transaction name must be printable, non-empty, at most "
                            + MAX_TRANSACTION_NAME_CHARS
                            + " characters, and unique among open transactions");
            return null;
        }
        String transactionId = (currentRequestId.isEmpty() ? "legacy" : currentRequestId)
                + "-tx-" + transactionSequence.incrementAndGet();
        TransactionState transaction = new TransactionState(
                normalized,
                transactionId,
                parentId,
                System.currentTimeMillis(),
                subTransaction);
        openTransactions.push(transaction);
        return transaction;
    }

    private static boolean validTransactionName(String name) {
        if (name == null || name.trim().isEmpty() || name.length() > MAX_TRANSACTION_NAME_CHARS) {
            return false;
        }
        for (int index = 0; index < name.length(); index++) {
            if (Character.isISOControl(name.charAt(index))) {
                return false;
            }
        }
        return true;
    }

    private static long transactionDurationMillis(double seconds) {
        if (Double.isNaN(seconds) || Double.isInfinite(seconds) || seconds < 0.0) {
            return -1L;
        }
        double milliseconds = seconds * 1000.0;
        if (Double.isInfinite(milliseconds) || milliseconds > MAX_TRANSACTION_DURATION_MS) {
            return -1L;
        }
        return Math.min(MAX_TRANSACTION_DURATION_MS, Math.round(milliseconds));
    }

    private static long durationMillis(double seconds) {
        if (Double.isNaN(seconds) || seconds <= 0.0) {
            return 0L;
        }
        double milliseconds = seconds * 1000.0;
        if (Double.isInfinite(milliseconds) || milliseconds >= MAX_TRANSACTION_DURATION_MS) {
            return MAX_TRANSACTION_DURATION_MS;
        }
        return Math.max(0L, Math.round(milliseconds));
    }

    private static long safeSubtract(long value, long nonnegativeDelta) {
        if (nonnegativeDelta <= 0L) {
            return value;
        }
        if (value < Long.MIN_VALUE + nonnegativeDelta) {
            return Long.MIN_VALUE;
        }
        return value - nonnegativeDelta;
    }

    private static long saturatingAddDuration(long current, long delta) {
        if (delta <= 0L) {
            return current;
        }
        if (current >= MAX_TRANSACTION_DURATION_MS - delta) {
            return MAX_TRANSACTION_DURATION_MS;
        }
        return current + delta;
    }

    private static long elapsedMillis(long startedAt, long endedAt) {
        if (endedAt <= startedAt) {
            return 0L;
        }
        // Both values originate from currentTimeMillis in ordinary open
        // transactions, but keep subtraction safe for hostile clock changes.
        if (startedAt < 0L && endedAt > Long.MAX_VALUE + startedAt) {
            return Long.MAX_VALUE;
        }
        return endedAt - startedAt;
    }

    private static long transactionRawDurationMillis(TransactionState transaction, long endedAt) {
        // Match lr.get_transaction_duration: this is wall-clock duration and
        // therefore includes both think and wasted time. The raw value remains
        // available as event evidence, while the reported transaction duration
        // subtracts lr.wasted_time below to match LoadRunner Analysis.
        return elapsedMillis(transaction.startedAtMillis, endedAt);
    }

    private static long reportedTransactionDurationMillis(long rawDurationMs, long wastedTimeMs) {
        long raw = Math.max(0L, rawDurationMs);
        long wasted = Math.max(0L, wastedTimeMs);
        return wasted >= raw ? 0L : raw - wasted;
    }

    private static void addThinkTimeToOpenTransactions(long millis) {
        if (millis <= 0L) {
            return;
        }
        for (TransactionState transaction : openTransactions) {
            transaction.thinkTimeMillis = saturatingAddDuration(transaction.thinkTimeMillis, millis);
        }
    }

    private static void addWastedTime(long suppliedMillis) {
        long millis = Math.max(0L, suppliedMillis);
        if (millis > 0L) {
            for (TransactionState transaction : openTransactions) {
                transaction.wastedTimeMillis = saturatingAddDuration(transaction.wastedTimeMillis, millis);
            }
        }
        emit("transaction_wasted_time", "", Long.toString(millis));
    }

    // LoadRunner continues after lr_end_transaction on a transaction that was
    // never started. Preserve that control flow while emitting a failed child
    // sample: otherwise an ignored FAIL return lets the parent action pass and
    // silently loses the malformed business transaction.
    private static void warnUnmatchedEnd(String api, String name) {
        String transactionId = (currentRequestId.isEmpty() ? "legacy" : currentRequestId)
                + "-unmatched-" + transactionSequence.incrementAndGet();
        String parentId = openTransactions.isEmpty() ? "" : openTransactions.peek().id;
        emitTransaction("transaction_unmatched", name, Integer.toString(FAIL), transactionId, parentId);
        emit("log", name, "lr." + api + ": transaction \"" + name
                + "\" was never started; continuing (LoadRunner log-and-continue semantics)");
    }

    private static void warnMissingSubTransactionParent(String name, String parentName) {
        String transactionId = (currentRequestId.isEmpty() ? "legacy" : currentRequestId)
                + "-unmatched-" + transactionSequence.incrementAndGet();
        emitTransaction("transaction_unmatched", name, Integer.toString(FAIL), transactionId, "");
        emit("log", name, "lr.start_sub_transaction: parent transaction \"" + parentName
                + "\" was never started; continuing (LoadRunner log-and-continue semantics)");
    }

    private static TransactionState currentTransaction(String name) {
        return currentTransaction(name, null);
    }

    private static TransactionState currentTransaction(String name, Boolean subTransaction) {
        String normalized = name == null ? "" : name;
        for (TransactionState transaction : openTransactions) {
            if (transaction.name.equals(normalized)
                    && (subTransaction == null || transaction.subTransaction == subTransaction.booleanValue())) {
                return transaction;
            }
        }
        return null;
    }

    private static int resolveTransactionStatus(TransactionState transaction, int requested) {
        // An explicit end status (LR_PASS/LR_FAIL/LR_STOP) always wins.
        if (requested != AUTO) {
            return requested;
        }
        return transaction.status;
    }

    private static boolean isConcreteTransactionStatus(int status) {
        return status == PASS || status == FAIL || status == STOP;
    }

    private static final class TransactionState {
        final String name;
        final String id;
        final String parentId;
        final long startedAtMillis;
        final boolean subTransaction;
        volatile int status = PASS;
        volatile long thinkTimeMillis;
        volatile long wastedTimeMillis;

        TransactionState(String name, String id, String parentId, long startedAtMillis, boolean subTransaction) {
            this.name = name;
            this.id = id;
            this.parentId = parentId == null ? "" : parentId;
            this.startedAtMillis = startedAtMillis;
            this.subTransaction = subTransaction;
        }
    }

    // Error (rather than RuntimeException) prevents ordinary user
    // catch(Exception) handlers from swallowing LoadRunner's control transfer.
    public static final class ExitSignal extends Error {
        private final int continuationOption;
        private final int exitStatus;

        ExitSignal(int continuationOption, int exitStatus) {
            super("lr.exit(" + continuationOption + ", " + exitStatus + ")");
            this.continuationOption = continuationOption;
            this.exitStatus = exitStatus;
        }

        public int continuationOption() {
            return continuationOption;
        }

        public int exitStatus() {
            return exitStatus;
        }

        @Override
        public synchronized Throwable fillInStackTrace() {
            return this;
        }
    }

    private static long effectiveThinkTimeMillis(long requestedMillis) {
        if (requestedMillis <= 0L) {
            return 0L;
        }
        // Runtime-settings escape hatches so the controller can disable or cap
        // think time for a stress run without re-importing the script.
        if (boolFromEnv("APEXLOAD_IGNORE_THINK_TIME")) {
            return 0L;
        }
        long limit = (long) intFromEnv("APEXLOAD_THINK_TIME_LIMIT_MS", 0);
        if (limit > 0L && requestedMillis > limit) {
            return limit;
        }
        return requestedMillis;
    }

    private static boolean boolFromEnv(String name) {
        String raw = setting(name);
        if (raw == null) {
            return false;
        }
        raw = raw.trim();
        return raw.equals("1") || raw.equalsIgnoreCase("true") || raw.equalsIgnoreCase("yes");
    }

    private static double doubleAt(Object[] args, int index, double fallback) {
        if (args == null || index < 0 || index >= args.length || args[index] == null) {
            return fallback;
        }
        Object value = args[index];
        if (value instanceof Number) {
            return ((Number) value).doubleValue();
        }
        try {
            return Double.parseDouble(String.valueOf(value).trim());
        } catch (NumberFormatException ex) {
            return fallback;
        }
    }

    private static int intFromEnv(String name, int fallback) {
        try {
            return Integer.parseInt(defaultString(setting(name), Integer.toString(fallback)).trim());
        } catch (NumberFormatException ex) {
            return fallback;
        }
    }

    private static String defaultString(String value, String fallback) {
        if (value == null || value.trim().isEmpty()) {
            return fallback;
        }
        return value;
    }

    private static String join(Object[] args) {
        if (args == null || args.length == 0) {
            return "";
        }
        StringBuilder out = new StringBuilder();
        for (int i = 0; i < args.length; i++) {
            if (i > 0) {
                out.append(' ');
            }
            out.append(valueAt(args, i));
        }
        return out.toString();
    }

    private static void emit(String type, String name, String message) {
        emitEvent(type, name, message, "");
    }

    private static void emitTransaction(String type, String name, String message, String transactionId) {
        emitEvent(type, name, message, transactionId, "");
    }

    private static void emitTransaction(String type, String name, String message, String transactionId, String parentTransactionId) {
        emitEvent(type, name, message, transactionId, parentTransactionId);
    }

    private static void emitEvent(String type, String name, String message, String transactionId) {
        emitEvent(type, name, message, transactionId, "");
    }

    private static void emitEvent(String type, String name, String message, String transactionId, String parentTransactionId) {
        emitEventAt(System.currentTimeMillis(), type, name, message, transactionId, parentTransactionId, -1L);
    }

    private static void emitTransactionAt(long timestampMs, String type, String name, String message,
                                          String transactionId, String parentTransactionId,
                                          long durationMs) {
        emitEventAt(timestampMs, type, name, message, transactionId, parentTransactionId,
                durationMs, -1L, -1L, -1L);
    }

    private static void emitTransactionAt(long timestampMs, String type, String name, String message,
                                          String transactionId, String parentTransactionId,
                                          long rawDurationMs, long thinkTimeMs, long wastedTimeMs) {
        emitEventAt(timestampMs, type, name, message, transactionId, parentTransactionId,
                reportedTransactionDurationMillis(rawDurationMs, wastedTimeMs),
                rawDurationMs, thinkTimeMs, wastedTimeMs);
    }

    private static void emitEventAt(long timestampMs, String type, String name, String message,
                                    String transactionId, String parentTransactionId,
                                    long durationMs) {
        emitEventAt(timestampMs, type, name, message, transactionId, parentTransactionId,
                durationMs, -1L, -1L, -1L);
    }

    private static void emitEventAt(long timestampMs, String type, String name, String message,
                                    String transactionId, String parentTransactionId,
                                    long durationMs, long rawDurationMs,
                                    long thinkTimeMs, long wastedTimeMs) {
        boolean attributed = !slotBound || (slotInvocationActive && Thread.currentThread() == slotInvocationThread);
        if (!attributed) slotAttributionFailed = true;
        long sequence = eventSequence.incrementAndGet();
        type = boundedEventField(type, MAX_EVENT_TYPE_CHARS);
        name = boundedEventField(name, MAX_EVENT_NAME_CHARS);
        message = boundedEventField(message, MAX_EVENT_MESSAGE_CHARS);
        String json = "{\"contract\":\"" + EVENT_CONTRACT
                + "\",\"request_id\":\"" + escape(attributed ? currentRequestId : "")
                + "\",\"timestamp_ms\":" + timestampMs
                + ",\"sequence\":" + sequence
                + ",\"type\":\"" + escape(type)
                + "\",\"name\":\"" + escape(name)
                + "\",\"message\":\"" + escape(message) + "\""
                + (transactionId == null || transactionId.isEmpty()
                    ? "" : ",\"transaction_id\":\"" + escape(transactionId) + "\"")
                + (parentTransactionId == null || parentTransactionId.isEmpty()
                    ? "" : ",\"parent_transaction_id\":\"" + escape(parentTransactionId) + "\"")
                + (durationMs < 0L ? "" : ",\"duration_ms\":" + durationMs)
                + (rawDurationMs < 0L ? "" : ",\"raw_duration_ms\":" + rawDurationMs)
                + (thinkTimeMs < 0L ? "" : ",\"think_time_ms\":" + thinkTimeMs)
                + (wastedTimeMs < 0L ? "" : ",\"wasted_time_ms\":" + wastedTimeMs)
                + "}";
        if (!attributed) {
            // Shared executors, child threads, and late work are host diagnostics.
            // Never credit them to the slot's current invocation.
            System.err.println(json);
            return;
        }
        String eventFile = eventFilePath();
        if (!eventFile.isEmpty()) {
            try {
                synchronized (lr.class) {
                    java.nio.file.Path path = Paths.get(eventFile);
                    byte[] line = (json + System.lineSeparator()).getBytes(StandardCharsets.UTF_8);
                    long size = Files.exists(path) ? Files.size(path) : 0L;
                    if (size >= MAX_EVENT_FILE_BYTES || line.length > MAX_EVENT_FILE_BYTES - size) {
                        eventCaptureTruncated = true;
                        return;
                    }
                    Files.write(path, line, StandardOpenOption.CREATE, StandardOpenOption.APPEND, StandardOpenOption.WRITE);
                }
                return;
            } catch (IOException ex) {
                eventFallbackUsed = true;
                System.err.println("{\"type\":\"error\",\"name\":\"event_file\",\"message\":\"" + escape(ex.toString()) + "\"}");
            }
        } else {
            eventFallbackUsed = true;
        }
        System.err.println(json);
    }

    private static String boundedEventField(String value, int maxChars) {
        if (value == null || value.length() <= maxChars) {
            return value == null ? "" : value;
        }
        return value.substring(0, maxChars) + "...truncated";
    }

    private static String eventFilePath() {
		String slotValue = slotContext.get("APEXLOAD_JAVA_VUSER_EVENT_FILE");
		if (slotBound) return slotValue == null ? "" : slotValue.trim();
        String value = System.getProperty("apexload.java_vuser.event_file");
        if (value == null || value.trim().isEmpty()) {
            value = System.getenv("APEXLOAD_JAVA_VUSER_EVENT_FILE");
        }
        return value == null ? "" : value.trim();
    }

    private static String escape(String value) {
        if (value == null) {
            return "";
        }
        StringBuilder sb = new StringBuilder(value.length() + 8);
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '\\': sb.append("\\\\"); break;
                case '"': sb.append("\\\""); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                case '\b': sb.append("\\b"); break;
                case '\f': sb.append("\\f"); break;
                default:
                    if (c < 0x20) {
                        // JSON requires all control characters be escaped; a raw tab
                        // or other control char produced output Go's json.Unmarshal
                        // rejected, silently dropping the event/transaction.
                        sb.append(String.format("\\u%04x", (int) c));
                    } else {
                        sb.append(c);
                    }
            }
        }
        return sb.toString();
    }
}
