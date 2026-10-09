Action()
{
	web_custom_request("form_post",
		"URL=http://parity.test/api/form",
		"Method=POST",
		"UserAgent=OrderAgent/2.1",
		"Body=a=1&b=2",
		"Body=&c=3",
		LAST);

	web_custom_request("json_post",
		"URL=http://parity.test/api/json",
		"Method=POST",
		"EncType=",
		"Body={\"k\":1}",
		LAST);

	web_custom_request("lookup",
		"URL=http://parity.test/api/lookup?id=7",
		"Method=GET",
		LAST);

	return 0;
}
