Action() {
 lr_start_transaction("flow");
 lr_start_sub_transaction("step", "flow");
 lr_think_time(3);
 web_global_verification("Text/IC=fixture-error", "Fail=Found", "Search=All", "ID=GlobalCheck", LAST);
 web_url("page", "URL=https://example.invalid/", "Mode=HTML", LAST);
 lr_end_sub_transaction("step", LR_AUTO);
 lr_end_transaction("flow", LR_AUTO);
 return 0;
}
