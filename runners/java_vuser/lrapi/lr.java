package lrapi;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.nio.file.StandardOpenOption;
import java.util.Base64;
import java.util.Deque;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentLinkedDeque;
import java.util.concurrent.atomic.AtomicLong;

public final class lr {
    public static final int PASS = 0;
    public static final int FAIL = 1;
    public static final int AUTO = 2;
    public static final int STOP = 3;
    public static final int EXIT_ACTION_AND_CONTINUE = 10;
    public static final int EXIT_ITERATION_AND_CONTINUE = 11;
    public static final int EXIT_VUSER = 12;
    public static final int EXIT_VUSER_AFTER_ITERATION = 13;

    private static final Map<String, String> parameters = new ConcurrentHashMap<>();
    private static final Map<String, Long> timers = new ConcurrentHashMap<>();
    // Explicit per-transaction status overrides set via set_transaction /
    // set_transaction_status. An entry here wins over LR_AUTO error inference.
    private static final Map<String, Integer> transactionStatus = new ConcurrentHashMap<>();
    // Value of errorCount observed when each still-open transaction started, so
    // LR_AUTO can fail a transaction that saw an error while it was open.
    private static final Map<String, Long> transactionStartErrors = new ConcurrentHashMap<>();
    // Currently-open transactions in start order (top = current transaction),
    // for lr_set_transaction_status's "current/open transaction" semantics.
    private static final Deque<String> openTransactions = new ConcurrentLinkedDeque<>();
    // Monotonic count of errors raised via lr.error_message during this run.
    private static final AtomicLong errorCount = new AtomicLong();

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

    public static void __apexload_set_parameter(String name, String value) {
        if (name == null || name.trim().isEmpty()) {
            return;
        }
        parameters.put(name.trim(), value == null ? "" : value);
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
        String name = valueAt(args, 0);
        int status = resolveTransactionStatus(name, intAt(args, 1, AUTO));
        emit("transaction_end", name, Integer.toString(status));
        closeTransaction(name);
        return status;
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
        String name = valueAt(args, 0);
        int status = resolveTransactionStatus(name, intAt(args, 1, AUTO));
        emit("transaction_end", name, Integer.toString(status));
        closeTransaction(name);
        return status;
    }

    public static int error_message(Object... args) {
        errorCount.incrementAndGet();
        emit("error", "", join(args));
        return FAIL;
    }

    public static byte[] eval_data(Object... args) {
        return eval_string(args).getBytes(StandardCharsets.UTF_8);
    }

    public static int eval_int(Object... args) {
        try {
            return Integer.parseInt(eval_string(args).trim());
        } catch (NumberFormatException ex) {
            return 0;
        }
    }

    public static String eval_string(String value) {
        if (value == null) {
            return "";
        }
        String out = value;
        for (Map.Entry<String, String> entry : parameters.entrySet()) {
            out = out.replace("{" + entry.getKey() + "}", entry.getValue());
        }
        return out;
    }

    public static String eval_string(Object... args) {
        return eval_string(valueAt(args, 0));
    }

    public static int exit(Object... args) {
        emit("runtime", "exit", join(args));
        return intAt(args, 0, EXIT_VUSER);
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
        return defaultString(System.getenv("APEXLOAD_GROUP_NAME"), "java_vuser");
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
        return defaultString(System.getenv("APEXLOAD_SCENARIO_ID"), "scenario");
    }

    public static double get_transaction_duration(Object... args) {
        return 0.0;
    }

    public static int get_transaction_status(Object... args) {
        return transactionStatus.getOrDefault(valueAt(args, 0), AUTO);
    }

    public static double get_transaction_think_time(Object... args) {
        return 0.0;
    }

    public static double get_transaction_wasted_time(Object... args) {
        return 0.0;
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

    public static int message(Object... args) {
        emit("log", "", join(args));
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
        if (args != null && args.length >= 2) {
            parameters.put(valueAt(args, 1), Integer.toString(intAt(args, 0, 0)));
        }
        return PASS;
    }

    public static int save_string(Object... args) {
        if (args != null && args.length >= 2) {
            String name = valueAt(args, 1);
            if (!name.isEmpty()) {
                parameters.put(name, valueAt(args, 0));
            }
        }
        return PASS;
    }

    public static int set_debug_message(Object... args) {
        emit("debug", "set_debug_message", join(args));
        return PASS;
    }

    public static int set_transaction(Object... args) {
        String name = valueAt(args, 0);
        int status = intAt(args, 2, AUTO);
        transactionStatus.put(name, status);
        emit("transaction_set", name, join(args));
        return status;
    }

    public static int set_transaction_status(Object... args) {
        int status = intAt(args, 0, AUTO);
        // lr_set_transaction_status sets the status of the open transaction(s).
        // Record it as an explicit override so a subsequent end_transaction with
        // LR_AUTO honors it instead of inferring pass/fail from error events.
        for (String name : openTransactions) {
            transactionStatus.put(name, status);
        }
        emit("transaction_status", "", Integer.toString(status));
        return status;
    }

    public static int sleep(Object... args) {
        return sleepMillis((long) doubleAt(args, 0, 0.0));
    }

    public static int start_sub_transaction(Object... args) {
        String name = valueAt(args, 0);
        openTransaction(name);
        emit("transaction_start", name, "sub");
        return PASS;
    }

    public static int start_timer(Object... args) {
        String name = valueAt(args, 0);
        timers.put(name, System.currentTimeMillis());
        emit("timer_start", name, "");
        return PASS;
    }

    public static int start_transaction(Object... args) {
        String name = valueAt(args, 0);
        openTransaction(name);
        emit("transaction_start", name, "");
        return PASS;
    }

    public static int think_time(double seconds) {
        long millis = Math.round(Math.max(0.0, seconds) * 1000.0);
        millis = effectiveThinkTimeMillis(millis);
        emit("think_time", "", Long.toString(millis));
        // The whole Vuser class runs inside a single java_vuser_run protocol
        // action, so the engine cannot re-insert pacing between lr.think_time
        // calls. The pause has to happen here in the JVM or it is lost entirely.
        if (millis > 0L) {
            return sleepMillis(millis);
        }
        return PASS;
    }

    public static int think_time(Object... args) {
        return think_time(doubleAt(args, 0, 0.0));
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

    public static int wasted_time(Object... args) {
        emit("transaction_wasted_time", "", join(args));
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

    private static void openTransaction(String name) {
        if (name == null) {
            name = "";
        }
        transactionStartErrors.put(name, errorCount.get());
        transactionStatus.remove(name);
        openTransactions.push(name);
    }

    private static void closeTransaction(String name) {
        if (name == null) {
            name = "";
        }
        openTransactions.remove(name);
        transactionStartErrors.remove(name);
        transactionStatus.remove(name);
    }

    private static int resolveTransactionStatus(String name, int requested) {
        // An explicit end status (LR_PASS/LR_FAIL/LR_STOP) always wins.
        if (requested != AUTO) {
            return requested;
        }
        // LR_AUTO: honor an explicit override from lr_set_transaction[_status]...
        Integer override = transactionStatus.get(name);
        if (override != null && override != AUTO) {
            return override;
        }
        // ...otherwise fail the transaction if an error was raised while it was
        // open (LoadRunner LR_AUTO semantics), else pass.
        Long startErrors = transactionStartErrors.get(name);
        if (startErrors != null && errorCount.get() > startErrors) {
            return FAIL;
        }
        return PASS;
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
        String raw = System.getenv(name);
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
            return Integer.parseInt(defaultString(System.getenv(name), Integer.toString(fallback)).trim());
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
        String json = "{\"type\":\"" + escape(type) + "\",\"name\":\"" + escape(name) + "\",\"message\":\"" + escape(message) + "\"}";
        String eventFile = eventFilePath();
        if (!eventFile.isEmpty()) {
            try {
                synchronized (lr.class) {
                    Files.write(Paths.get(eventFile), (json + System.lineSeparator()).getBytes(StandardCharsets.UTF_8), StandardOpenOption.CREATE, StandardOpenOption.APPEND, StandardOpenOption.WRITE);
                }
                return;
            } catch (IOException ex) {
                System.err.println("{\"type\":\"error\",\"name\":\"event_file\",\"message\":\"" + escape(ex.toString()) + "\"}");
            }
        }
        System.err.println(json);
    }

    private static String eventFilePath() {
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
