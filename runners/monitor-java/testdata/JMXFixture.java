import java.lang.management.ManagementFactory;
import java.rmi.registry.*;
import java.util.*;
import javax.management.remote.*;
import javax.rmi.ssl.SslRMIServerSocketFactory;

public final class JMXFixture {
    public static void main(String[] args) throws Exception {
        int port=Integer.parseInt(args[0]);
        System.setProperty("java.rmi.server.hostname","127.0.0.1");
        SslRMIServerSocketFactory sockets=new SslRMIServerSocketFactory();
        Registry registry=LocateRegistry.createRegistry(port,null,sockets);
        Map<String,Object> environment=new HashMap<>();
        environment.put("jmx.remote.rmi.server.socket.factory",sockets);
        environment.put(JMXConnectorServer.AUTHENTICATOR,(JMXAuthenticator) credentials -> {
            String[] login=(String[])credentials;
            if(login.length!=2 || !login[0].equals("monitor") || !login[1].equals("fixture-secret")) throw new SecurityException("denied");
            return new javax.security.auth.Subject();
        });
        JMXConnectorServer server=JMXConnectorServerFactory.newJMXConnectorServer(new JMXServiceURL("service:jmx:rmi://127.0.0.1:"+port),environment,ManagementFactory.getPlatformMBeanServer());
        server.start();
        // Direct in-process registry reference avoids a fixture-only TLS client.
        String address=server.getAddress().getURLPath();
        byte[] bytes=Base64.getDecoder().decode(address.substring("/stub/".length()));
        Object stub=new java.io.ObjectInputStream(new java.io.ByteArrayInputStream(bytes)).readObject();
        registry.rebind("jmxrmi",(java.rmi.Remote)stub);
        System.out.println("READY");System.out.flush();
        System.in.read();server.stop();java.rmi.server.UnicastRemoteObject.unexportObject(registry,true);
    }
}
