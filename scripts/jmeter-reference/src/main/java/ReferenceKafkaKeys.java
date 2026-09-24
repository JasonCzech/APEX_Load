import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import com.di.jmeter.kafka.sampler.KafkaProducerSampler;
import org.apache.jmeter.threads.JMeterContextService;
import org.apache.jmeter.threads.JMeterVariables;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.ProducerRecord;

/** Exercises the pinned sampler's record construction without a producer/network. */
public final class ReferenceKafkaKeys {
 public static void main(String[] args) throws Exception {
  if (!Runtime.version().toString().equals("17.0.16+8")) throw new IllegalStateException("Pinned JDK required");
  JMeterVariables variables = new JMeterVariables();
  // This revision leaves both config/sampler serializer variable-name fields null.
  // Its config stores the StringSerializer value under that same null key.
  variables.put(null,"org.apache.kafka.common.serialization.StringSerializer");
  JMeterContextService.getContext().setVariables(variables);
  Method build = KafkaProducerSampler.class.getDeclaredMethod("getProducerRecord",KafkaProducer.class);
  build.setAccessible(true);
  System.out.print("{\"vectors\":["); boolean first=true;
  for (String key: new String[]{""," ","key","café"}) {
   KafkaProducerSampler<String,String> sampler = new KafkaProducerSampler<>();
   sampler.setKafkaTopic("synthetic-topic"); sampler.setKafkaMessageKey(key);
   sampler.setKafkaMessage("synthetic-payload"); sampler.setPartitionString("2");
   ProducerRecord<?,?> record = (ProducerRecord<?,?>)build.invoke(sampler,new Object[]{null});
   if (!first) System.out.print(","); first=false;
   String encoded=Base64.getEncoder().encodeToString(key.getBytes(StandardCharsets.UTF_8));
   System.out.print("[\""+encoded+"\","+ (record.key()!=null) +","+record.partition()+"]");
  }
  System.out.println("]}");
 }
}
