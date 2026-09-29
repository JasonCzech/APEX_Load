import lrapi.lr;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;

/** Deterministic fixture; the SUT is a separate process outside accounting. */
public final class LightActions {
    private String endpoint;
    public int init() {
        endpoint = lr.eval_string("{Endpoint}");
        if (!endpoint.startsWith("http://") && !endpoint.startsWith("https://"))
            throw new IllegalArgumentException("fixture Endpoint is required");
        return 0;
    }
    public int action() throws Exception {
        lr.start_transaction("order-flow");
        HttpURLConnection connection = (HttpURLConnection)new URL(endpoint + "/order").openConnection();
        connection.setConnectTimeout(5000);
        connection.setReadTimeout(5000);
        try {
            if (connection.getResponseCode() != 200) throw new IllegalStateException("fixture HTTP status");
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            try (InputStream stream = connection.getInputStream()) {
                byte[] buffer = new byte[4096]; int n;
                while ((n = stream.read(buffer)) >= 0) {
                    if (bytes.size() + n > 65536) throw new IllegalStateException("fixture response too large");
                    bytes.write(buffer, 0, n);
                }
            }
            if (!new String(bytes.toByteArray(), "UTF-8").contains("\"id\":17"))
                throw new IllegalStateException("fixture response mismatch");
        } finally { connection.disconnect(); }
        lr.end_transaction("order-flow", lr.PASS);
        return 0;
    }
    public int end() { return 0; }
}
