package generatorrpc

// FeaturePreparationV1 guarantees assignment staging defers protocol and
// runtime preflight when prepare_only is requested. CommitWork still validates
// runtimes before opening the existing execution gate.
const FeaturePreparationV1 = "prepare_v1"
