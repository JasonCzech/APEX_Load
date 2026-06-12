import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

public final class lr {
    private static final Map<String, String> parameters = new ConcurrentHashMap<>();

    private lr() {
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

    public static void save_string(String value, String name) {
        if (name != null) {
            parameters.put(name, value == null ? "" : value);
        }
    }

    public static void setParam(String name, String value) {
        save_string(value, name);
    }

    public static String getParam(String name) {
        return parameters.getOrDefault(name, "");
    }

    public static int start_transaction(String name) {
        emit("transaction_start", name, "");
        return 0;
    }

    public static int end_transaction(String name, int status) {
        emit("transaction_end", name, Integer.toString(status));
        return status;
    }

    public static int think_time(double seconds) {
        long millis = Math.max(0L, Math.round(seconds * 1000.0));
        try {
            Thread.sleep(millis);
            return 0;
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            return 1;
        }
    }

    public static void log_message(String message) {
        emit("log", "", message);
    }

    public static void output_message(String message) {
        emit("log", "", message);
    }

    public static void error_message(String message) {
        emit("error", "", message);
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
