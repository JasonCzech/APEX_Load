import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;

public final class Harness {
    private Harness() {
    }

    public static void main(String[] args) throws Exception {
        Command command = Command.from(args, readCommandLine());
        Object actions = Class.forName(command.className).getDeclaredConstructor().newInstance();
        long start = System.currentTimeMillis();
        try {
            if ("init".equals(command.method)) {
                invokeIfPresent(actions, "init");
            } else if ("end".equals(command.method)) {
                invokeIfPresent(actions, "end");
            } else if ("action".equals(command.method)) {
                invokeIfPresent(actions, "action");
            } else {
                invokeIfPresent(actions, "init");
                invokeIfPresent(actions, "action");
                invokeIfPresent(actions, "end");
            }
            long duration = System.currentTimeMillis() - start;
            System.out.println("{\"success\":true,\"class_name\":\"" + escape(command.className) + "\",\"method\":\"" + escape(command.method) + "\",\"duration_ms\":" + duration + "}");
        } catch (InvocationTargetException ex) {
            Throwable cause = ex.getCause() == null ? ex : ex.getCause();
            System.out.println("{\"success\":false,\"error\":\"" + escape(cause.toString()) + "\"}");
            System.exit(1);
        }
    }

    private static void invokeIfPresent(Object target, String name) throws Exception {
        Method method;
        try {
            method = target.getClass().getMethod(name);
        } catch (NoSuchMethodException ex) {
            return;
        }
        method.invoke(target);
    }

    private static String readCommandLine() throws Exception {
        BufferedReader reader = new BufferedReader(new InputStreamReader(System.in));
        String line = reader.readLine();
        return line == null ? "" : line;
    }

    private static String escape(String value) {
        if (value == null) {
            return "";
        }
        return value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\r", "\\r");
    }

    private static final class Command {
        String className = "Actions";
        String method = "full_lifecycle";

        static Command from(String[] args, String jsonLine) {
            Command command = new Command();
            command.className = firstNonEmpty(argValue(args, "--class"), jsonString(jsonLine, "class_name"), command.className);
            command.method = firstNonEmpty(argValue(args, "--method"), jsonString(jsonLine, "method"), command.method);
            return command;
        }

        private static String argValue(String[] args, String key) {
            for (int i = 0; i + 1 < args.length; i++) {
                if (key.equals(args[i])) {
                    return args[i + 1];
                }
            }
            return "";
        }

        private static String jsonString(String json, String key) {
            String needle = "\"" + key + "\"";
            int keyIndex = json.indexOf(needle);
            if (keyIndex < 0) {
                return "";
            }
            int colon = json.indexOf(':', keyIndex + needle.length());
            if (colon < 0) {
                return "";
            }
            int start = json.indexOf('"', colon + 1);
            if (start < 0) {
                return "";
            }
            StringBuilder out = new StringBuilder();
            boolean escaped = false;
            for (int i = start + 1; i < json.length(); i++) {
                char ch = json.charAt(i);
                if (escaped) {
                    out.append(ch);
                    escaped = false;
                    continue;
                }
                if (ch == '\\') {
                    escaped = true;
                    continue;
                }
                if (ch == '"') {
                    return out.toString();
                }
                out.append(ch);
            }
            return "";
        }

        private static String firstNonEmpty(String... values) {
            for (String value : values) {
                if (value != null && !value.trim().isEmpty()) {
                    return value.trim();
                }
            }
            return "";
        }
    }
}
