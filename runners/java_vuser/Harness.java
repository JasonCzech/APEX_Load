import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

import lrapi.lr;

public final class Harness {
    private Harness() {
    }

    public static void main(String[] args) throws Exception {
        Command command = Command.from(args, readCommandLine());
        if (!command.sourceArchiveRef.isEmpty()) {
            System.setProperty("apexload.java_vuser.source_archive_ref", command.sourceArchiveRef);
        }
        lr.__apexload_seed_parameters(command.lrParameters);
        long start = System.currentTimeMillis();
        try {
            Object actions = instantiateActions(command);
            RunResult runResult = new RunResult();
            if ("init".equals(command.method)) {
                runResult.required(invokeIfPresent(actions, "init"), "init");
            } else if ("end".equals(command.method)) {
                runResult.required(invokeIfPresent(actions, "end"), "end");
            } else if ("action".equals(command.method)) {
                runResult.required(invokeIfPresent(actions, "action"), "action");
            } else {
                runResult.optional(invokeIfPresent(actions, "init"));
                runResult.required(invokeIfPresent(actions, "action"), "action");
                runResult.optional(invokeIfPresent(actions, "end"));
            }
            long duration = System.currentTimeMillis() - start;
            System.out.println("{\"success\":true,\"class_name\":\"" + escape(command.className) + "\",\"method\":\"" + escape(command.method) + "\",\"duration_ms\":" + duration + "}");
        } catch (InvocationTargetException ex) {
            Throwable cause = ex.getCause() == null ? ex : ex.getCause();
            System.out.println("{\"success\":false,\"error\":\"" + escape(cause.toString()) + "\"}");
            System.exit(1);
        } catch (RuntimeException ex) {
            System.out.println("{\"success\":false,\"error\":\"" + escape(ex.toString()) + "\"}");
            System.exit(1);
        }
    }

    private static InvocationResult invokeIfPresent(Object target, String name) throws Exception {
        Method method;
        try {
            method = target.getClass().getMethod(name);
        } catch (NoSuchMethodException ex) {
            return InvocationResult.missing(name);
        }
        Object returned = method.invoke(target);
        int status = lr.PASS;
        if (returned instanceof Number) {
            status = ((Number) returned).intValue();
        }
        return InvocationResult.invoked(name, status);
    }

    private static Object instantiateActions(Command command) throws Exception {
        if (command.startupTimeoutMs <= 0) {
            return Class.forName(command.className).getDeclaredConstructor().newInstance();
        }
        ExecutorService executor = Executors.newSingleThreadExecutor();
        Future<Object> future = executor.submit(() -> Class.forName(command.className).getDeclaredConstructor().newInstance());
        try {
            return future.get(command.startupTimeoutMs, TimeUnit.MILLISECONDS);
        } catch (TimeoutException ex) {
            future.cancel(true);
            throw new IllegalStateException("startup timeout after " + command.startupTimeoutMs + "ms while loading " + command.className);
        } catch (ExecutionException ex) {
            Throwable cause = ex.getCause();
            if (cause instanceof Exception) {
                throw (Exception) cause;
            }
            if (cause instanceof Error) {
                throw (Error) cause;
            }
            throw new RuntimeException(cause);
        } finally {
            executor.shutdownNow();
        }
    }

    private static final class RunResult {
        void optional(InvocationResult result) {
            if (result.invoked) {
                requirePass(result);
            }
        }

        void required(InvocationResult result, String name) {
            if (!result.invoked) {
                throw new IllegalStateException("required zero-argument lifecycle method was not found: " + name);
            }
            requirePass(result);
        }

        private void requirePass(InvocationResult result) {
            if (result.status != lr.PASS) {
                throw new IllegalStateException("lifecycle method " + result.name + " returned non-PASS status " + result.status);
            }
        }
    }

    private static final class InvocationResult {
        final String name;
        final boolean invoked;
        final int status;

        private InvocationResult(String name, boolean invoked, int status) {
            this.name = name;
            this.invoked = invoked;
            this.status = status;
        }

        static InvocationResult missing(String name) {
            return new InvocationResult(name, false, lr.PASS);
        }

        static InvocationResult invoked(String name, int status) {
            return new InvocationResult(name, true, status);
        }
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
        String sourceArchiveRef = "";
        int startupTimeoutMs = 0;
        Map<String, String> lrParameters = new LinkedHashMap<>();

        static Command from(String[] args, String jsonLine) {
            Command command = new Command();
            command.className = firstNonEmpty(argValue(args, "--class"), jsonString(jsonLine, "class_name"), command.className);
            command.method = firstNonEmpty(argValue(args, "--method"), jsonString(jsonLine, "method"), command.method);
            command.sourceArchiveRef = firstNonEmpty(argValue(args, "--source-archive-ref"), jsonString(jsonLine, "source_archive_ref"));
            command.startupTimeoutMs = parsePositiveInt(firstNonEmpty(argValue(args, "--startup-timeout-ms"), jsonNumber(jsonLine, "startup_timeout_ms")));
            String paramsObject = jsonObject(jsonLine, "params");
            command.lrParameters.putAll(jsonStringMap(jsonObject(paramsObject, "parameters")));
            command.lrParameters.putAll(jsonStringMap(jsonObject(paramsObject, "lr_parameters")));
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
            ParsedString parsed = parseJsonString(json, start);
            return parsed == null ? "" : parsed.value;
        }

        private static String jsonNumber(String json, String key) {
            if (json == null || json.isEmpty()) {
                return "";
            }
            String needle = "\"" + key + "\"";
            int keyIndex = json.indexOf(needle);
            if (keyIndex < 0) {
                return "";
            }
            int colon = json.indexOf(':', keyIndex + needle.length());
            if (colon < 0) {
                return "";
            }
            int start = nextNonWhitespace(json, colon + 1);
            if (start < 0) {
                return "";
            }
            int end = start;
            while (end < json.length()) {
                char ch = json.charAt(end);
                if ((ch >= '0' && ch <= '9') || ch == '-') {
                    end++;
                    continue;
                }
                break;
            }
            return end > start ? json.substring(start, end) : "";
        }

        private static int parsePositiveInt(String value) {
            if (value == null || value.isEmpty()) {
                return 0;
            }
            try {
                int parsed = Integer.parseInt(value.trim());
                return parsed > 0 ? parsed : 0;
            } catch (NumberFormatException ex) {
                return 0;
            }
        }

        private static String jsonObject(String json, String key) {
            if (json == null || json.isEmpty()) {
                return "";
            }
            String needle = "\"" + key + "\"";
            int keyIndex = json.indexOf(needle);
            if (keyIndex < 0) {
                return "";
            }
            int colon = json.indexOf(':', keyIndex + needle.length());
            if (colon < 0) {
                return "";
            }
            int start = nextNonWhitespace(json, colon + 1);
            if (start < 0 || json.charAt(start) != '{') {
                return "";
            }
            int end = scanCompositeEnd(json, start, '{', '}');
            return end < 0 ? "" : json.substring(start, end + 1);
        }

        private static Map<String, String> jsonStringMap(String jsonObject) {
            Map<String, String> out = new LinkedHashMap<>();
            if (jsonObject == null || jsonObject.trim().isEmpty()) {
                return out;
            }
            int i = nextNonWhitespace(jsonObject, 0);
            if (i < 0 || jsonObject.charAt(i) != '{') {
                return out;
            }
            i++;
            while (i < jsonObject.length()) {
                i = skipWhitespaceAndCommas(jsonObject, i);
                if (i < 0 || i >= jsonObject.length() || jsonObject.charAt(i) == '}') {
                    return out;
                }
                ParsedString key = parseJsonString(jsonObject, i);
                if (key == null) {
                    return out;
                }
                i = skipWhitespace(jsonObject, key.next);
                if (i < 0 || i >= jsonObject.length() || jsonObject.charAt(i) != ':') {
                    return out;
                }
                i = skipWhitespace(jsonObject, i + 1);
                if (i < 0 || i >= jsonObject.length()) {
                    return out;
                }
                String value;
                if (jsonObject.charAt(i) == '"') {
                    ParsedString parsedValue = parseJsonString(jsonObject, i);
                    if (parsedValue == null) {
                        return out;
                    }
                    value = parsedValue.value;
                    i = parsedValue.next;
                } else if (jsonObject.charAt(i) == '{') {
                    int end = scanCompositeEnd(jsonObject, i, '{', '}');
                    if (end < 0) {
                        return out;
                    }
                    value = jsonObject.substring(i, end + 1);
                    i = end + 1;
                } else if (jsonObject.charAt(i) == '[') {
                    int end = scanCompositeEnd(jsonObject, i, '[', ']');
                    if (end < 0) {
                        return out;
                    }
                    value = jsonObject.substring(i, end + 1);
                    i = end + 1;
                } else {
                    int end = i;
                    while (end < jsonObject.length() && jsonObject.charAt(end) != ',' && jsonObject.charAt(end) != '}') {
                        end++;
                    }
                    value = jsonObject.substring(i, end).trim();
                    if ("null".equals(value)) {
                        value = "";
                    }
                    i = end;
                }
                out.put(key.value, value);
            }
            return out;
        }

        private static ParsedString parseJsonString(String json, int start) {
            if (start < 0 || start >= json.length() || json.charAt(start) != '"') {
                return null;
            }
            StringBuilder out = new StringBuilder();
            boolean escaped = false;
            for (int i = start + 1; i < json.length(); i++) {
                char ch = json.charAt(i);
                if (escaped) {
                    if (ch == 'n') {
                        out.append('\n');
                    } else if (ch == 'r') {
                        out.append('\r');
                    } else if (ch == 't') {
                        out.append('\t');
                    } else if (ch == 'b') {
                        out.append('\b');
                    } else if (ch == 'f') {
                        out.append('\f');
                    } else if (ch == 'u' && i + 4 < json.length()) {
                        String hex = json.substring(i + 1, i + 5);
                        try {
                            out.append((char) Integer.parseInt(hex, 16));
                            i += 4;
                        } catch (NumberFormatException ex) {
                            out.append("\\u").append(hex);
                            i += 4;
                        }
                    } else {
                        out.append(ch);
                    }
                    escaped = false;
                    continue;
                }
                if (ch == '\\') {
                    escaped = true;
                    continue;
                }
                if (ch == '"') {
                    return new ParsedString(out.toString(), i + 1);
                }
                out.append(ch);
            }
            return null;
        }

        private static int scanCompositeEnd(String json, int start, char open, char close) {
            int depth = 0;
            boolean inString = false;
            boolean escaped = false;
            for (int i = start; i < json.length(); i++) {
                char ch = json.charAt(i);
                if (inString) {
                    if (escaped) {
                        escaped = false;
                    } else if (ch == '\\') {
                        escaped = true;
                    } else if (ch == '"') {
                        inString = false;
                    }
                    continue;
                }
                if (ch == '"') {
                    inString = true;
                    continue;
                }
                if (ch == open) {
                    depth++;
                } else if (ch == close) {
                    depth--;
                    if (depth == 0) {
                        return i;
                    }
                }
            }
            return -1;
        }

        private static int nextNonWhitespace(String value, int start) {
            int i = skipWhitespace(value, start);
            if (i >= value.length()) {
                return -1;
            }
            return i;
        }

        private static int skipWhitespaceAndCommas(String value, int start) {
            int i = start;
            while (i < value.length()) {
                char ch = value.charAt(i);
                if (Character.isWhitespace(ch) || ch == ',') {
                    i++;
                    continue;
                }
                return i;
            }
            return -1;
        }

        private static int skipWhitespace(String value, int start) {
            int i = start;
            while (i < value.length() && Character.isWhitespace(value.charAt(i))) {
                i++;
            }
            return i;
        }

        private static String firstNonEmpty(String... values) {
            for (String value : values) {
                if (value != null && !value.trim().isEmpty()) {
                    return value.trim();
                }
            }
            return "";
        }

        private static final class ParsedString {
            final String value;
            final int next;

            ParsedString(String value, int next) {
                this.value = value;
                this.next = next;
            }
        }
    }
}
