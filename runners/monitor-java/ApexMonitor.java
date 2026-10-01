import java.io.*;
import java.net.*;
import java.rmi.server.RMISocketFactory;
import java.sql.*;
import java.util.*;
import javax.management.*;
import javax.management.remote.*;
import javax.net.ssl.*;

/** No expressions, user SQL, dynamic classes, or invocations are accepted. */
public final class ApexMonitor {
    private static void metric(String name, String unit, double value) {
        if (!Double.isFinite(value)) throw new IllegalArgumentException("nonfinite metric");
        System.out.print("{\"metric_name\":\""+name+"\",\"unit\":\""+unit+"\",\"kind\":\"gauge\",\"value\":"+value+"}");
    }
    public static void main(String[] args) throws Exception {
        if (args.length < 3 || args.length > 4) throw new IllegalArgumentException("pack arguments required");
        BufferedReader input = new BufferedReader(new InputStreamReader(System.in, java.nio.charset.StandardCharsets.UTF_8));
        String user = input.readLine(), password = input.readLine();
        if (user == null || password == null || user.length() > 128 || password.length() > 16384) throw new IllegalArgumentException("credentials required");
        if (args[0].equals("jdbc") && args[2].equals("connection-health-v1")) {
            URI uri = URI.create(args[1]);
            if (!uri.getScheme().equals("postgresql") || uri.getUserInfo()!=null || uri.getQuery()!=null || uri.getFragment()!=null) throw new IllegalArgumentException("approved PostgreSQL endpoint required");
            // Driver must be an operator-approved, version-pinned PostgreSQL jar.
            Class.forName("org.postgresql.Driver");
            Properties properties = new Properties();
            properties.setProperty("user",user); properties.setProperty("password",password);
            if(args.length==4 && !args[3].isEmpty()) properties.setProperty("sslrootcert",args[3]);
            properties.setProperty("sslmode","verify-full"); properties.setProperty("connectTimeout","5"); properties.setProperty("socketTimeout","5");
            long start=System.nanoTime();
            try(Connection connection=DriverManager.getConnection("jdbc:"+args[1],properties)) {
                connection.setReadOnly(true); connection.setAutoCommit(false);
                try(Statement statement=connection.createStatement()) {
                    statement.setQueryTimeout(5); statement.setMaxRows(1);
                    try(ResultSet rows=statement.executeQuery("SELECT 1")) {
                        if (!rows.next() || rows.getInt(1)!=1) throw new SQLException("health query failed");
                    }
                } finally { connection.rollback(); }
                System.out.print("["); metric("jdbc.connection_health.duration","ms",(System.nanoTime()-start)/1e6);System.out.print("]");
            }
        } else if(args[0].equals("jmx") && args[2].equals("jvm-basic-v1")) {
            URI uri=URI.create(args[1]);
            if (!uri.getScheme().equals("jmx") || uri.getHost()==null || uri.getPort()<1 || uri.getUserInfo()!=null || uri.getQuery()!=null || uri.getFragment()!=null || !uri.getPath().isEmpty()) throw new IllegalArgumentException("approved JMX endpoint required");
            String host=uri.getHost(); int port=uri.getPort();
            ObjectInputFilter.Config.setSerialFilter(info -> {
                if(info.depth()>20 || info.references()>10000 || info.streamBytes()>1048576 || info.arrayLength()>10000) return ObjectInputFilter.Status.REJECTED;
                Class<?> type=info.serialClass();
                if(type!=null && java.rmi.server.RMIClientSocketFactory.class.isAssignableFrom(type)) return ObjectInputFilter.Status.REJECTED;
                return ObjectInputFilter.Status.UNDECIDED;
            });
            // Registry and server must use the same explicitly pinned TLS port.
            // Remote stubs cannot redirect credentials to another host or port.
            RMISocketFactory.setSocketFactory(new RMISocketFactory() {
                public Socket createSocket(String h,int p) throws IOException {
                    if (!h.equalsIgnoreCase(host) || p!=port) throw new IOException("unapproved RMI destination");
                    SSLSocket socket=(SSLSocket)SSLSocketFactory.getDefault().createSocket();
                    SSLParameters parameters=socket.getSSLParameters(); parameters.setEndpointIdentificationAlgorithm("HTTPS");socket.setSSLParameters(parameters);
                    socket.connect(new InetSocketAddress(h,p),5000);socket.setSoTimeout(5000);socket.startHandshake();return socket;
                }
                public ServerSocket createServerSocket(int p) throws IOException { throw new IOException("listeners prohibited"); }
            });
            Map<String,Object> environment=new HashMap<>();
            environment.put(JMXConnector.CREDENTIALS,new String[]{user,password});
            environment.put("jmx.remote.x.request.waiting.timeout",5000L);
            environment.put("com.sun.jndi.rmi.factory.socket",RMISocketFactory.getSocketFactory());
            JMXServiceURL service=new JMXServiceURL("service:jmx:rmi:///jndi/rmi://"+uri.getRawAuthority()+"/jmxrmi");
            try(JMXConnector connector=JMXConnectorFactory.connect(service,environment)) {
                MBeanServerConnection server=connector.getMBeanServerConnection();
                Number threads=(Number)server.getAttribute(new ObjectName("java.lang:type=Threading"),"ThreadCount");
                Number uptime=(Number)server.getAttribute(new ObjectName("java.lang:type=Runtime"),"Uptime");
                javax.management.openmbean.CompositeData heap=(javax.management.openmbean.CompositeData)server.getAttribute(new ObjectName("java.lang:type=Memory"),"HeapMemoryUsage");
                System.out.print("[");metric("jvm.threads","1",threads.doubleValue());System.out.print(",");metric("jvm.uptime","ms",uptime.doubleValue());System.out.print(",");metric("jvm.heap.used","By",((Number)heap.get("used")).doubleValue());System.out.print("]");
            }
        } else throw new IllegalArgumentException("unapproved pack");
    }
}
