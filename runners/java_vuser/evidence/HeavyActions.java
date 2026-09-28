import lrapi.lr;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.common.hash.Hashing;
import java.nio.charset.StandardCharsets;
import org.apache.http.client.config.RequestConfig;
import org.apache.http.client.methods.CloseableHttpResponse;
import org.apache.http.client.methods.HttpGet;
import org.apache.http.client.methods.HttpPost;
import org.apache.http.entity.StringEntity;
import org.apache.http.impl.client.CloseableHttpClient;
import org.apache.http.impl.client.HttpClients;
import org.apache.http.impl.conn.PoolingHttpClientConnectionManager;
import org.apache.http.util.EntityUtils;

/** Middleware-heavy fixture. State and connections belong to each Actions. */
public final class HeavyActions {
    private String endpoint;
    private CloseableHttpClient client;
    private ObjectMapper mapper;
    public int init() {
        endpoint = lr.eval_string("{Endpoint}");
        if (!endpoint.startsWith("http://") && !endpoint.startsWith("https://"))
            throw new IllegalArgumentException("fixture Endpoint is required");
        mapper = new ObjectMapper();
        PoolingHttpClientConnectionManager connections = new PoolingHttpClientConnectionManager();
        connections.setMaxTotal(2);
        connections.setDefaultMaxPerRoute(2);
        client = HttpClients.custom().setConnectionManager(connections).setDefaultRequestConfig(
            RequestConfig.custom().setConnectTimeout(5000).setSocketTimeout(5000).setConnectionRequestTimeout(5000).build()).build();
        return 0;
    }
    public int action() throws Exception {
        lr.start_transaction("order-flow");
        String order;
        try (CloseableHttpResponse response = client.execute(new HttpGet(endpoint + "/order"))) {
            if (response.getStatusLine().getStatusCode() != 200) throw new IllegalStateException("fixture GET status");
            order = EntityUtils.toString(response.getEntity(), StandardCharsets.UTF_8);
            JsonNode value = mapper.readTree(order);
            if (value.path("id").asInt() != 17 || value.path("payload").asText().length() != 4096)
                throw new IllegalStateException("fixture GET body");
        }
        HttpPost post = new HttpPost(endpoint + "/order");
        post.setHeader("X-Content-SHA256", Hashing.sha256().hashString(order, StandardCharsets.UTF_8).toString());
        post.setEntity(new StringEntity(order, StandardCharsets.UTF_8));
        try (CloseableHttpResponse response = client.execute(post)) {
            if (response.getStatusLine().getStatusCode() != 200 || !EntityUtils.toString(response.getEntity()).equals("ok"))
                throw new IllegalStateException("fixture POST mismatch");
        }
        lr.end_transaction("order-flow", lr.PASS);
        return 0;
    }
    public int end() throws Exception { if (client != null) client.close(); return 0; }
}
