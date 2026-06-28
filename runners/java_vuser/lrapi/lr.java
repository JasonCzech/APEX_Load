package lrapi;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.Base64;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

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
    private static final Map<String, Integer> transactionStatus = new ConcurrentHashMap<>();

    private lr() {
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
        int status = intAt(args, 1, AUTO);
        emit("transaction_end", name, Integer.toString(status));
        transactionStatus.put(name, status);
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
        int status = intAt(args, 1, AUTO);
        emit("transaction_end", name, Integer.toString(status));
        transactionStatus.put(name, status);
        return status;
    }

    public static int error_message(Object... args) {
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
        emit("transaction_status", "", Integer.toString(status));
        return status;
    }

    public static int sleep(Object... args) {
        return sleepMillis((long) doubleAt(args, 0, 0.0));
    }

    public static int start_sub_transaction(Object... args) {
        String name = valueAt(args, 0);
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
        emit("transaction_start", name, "");
        return PASS;
    }

    public static int think_time(double seconds) {
        return sleepMillis(Math.round(Math.max(0.0, seconds) * 1000.0));
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
        System.err.println("{\"type\":\"" + escape(type) + "\",\"name\":\"" + escape(name) + "\",\"message\":\"" + escape(message) + "\"}");
    }

    private static String escape(String value) {
        if (value == null) {
            return "";
        }
        return value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\r", "\\r");
    }
}
