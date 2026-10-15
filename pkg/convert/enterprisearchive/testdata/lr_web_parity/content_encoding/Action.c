Action()
{
	lr_start_transaction("upload");

	web_custom_request("compressed",
		"URL=http://parity.test/api/bulk",
		"Method=POST",
		"EncType=application/json",
		"ContentEncoding=gzip",
		"Body={\"rows\":[1,2,3]}",
		LAST);

	web_custom_request("deflated",
		"URL=http://parity.test/api/bulk",
		"Method=POST",
		"EncType=application/json",
		"ContentEncoding=deflate",
		"Body={\"rows\":[4]}",
		LAST);

	lr_end_transaction("upload", LR_AUTO);

	return 0;
}
