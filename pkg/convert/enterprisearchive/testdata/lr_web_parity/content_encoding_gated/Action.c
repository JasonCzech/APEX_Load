Action()
{
	web_custom_request("compressed",
		"URL=http://parity.test/api/bulk",
		"Method=POST",
		"EncType=application/json",
		"ContentEncoding=gzip",
		"Body={\"rows\":[1,2,3]}",
		LAST);

	return 0;
}
