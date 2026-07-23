public final class SmokeActions {
    public int action() {
        if (!"ci".equals(System.getProperty("java_vuser.smoke"))) {
            throw new IllegalStateException("expected exact Java VM system property");
        }
        if (!"/tmp".equals(System.getProperty("user.dir"))) {
            throw new IllegalStateException("expected the bounded writable JVM work directory");
        }
        return 0;
    }
}
