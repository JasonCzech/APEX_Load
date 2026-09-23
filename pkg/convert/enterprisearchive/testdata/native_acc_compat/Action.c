Action(){lr_start_transaction("Open");web_url("Open", "URL=https://example.invalid/", "Resource=0", LAST);lr_end_transaction("Open", LR_AUTO);return 0;}
