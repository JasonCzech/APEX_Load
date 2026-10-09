Action()
{
	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		"Referer=",
		"Mode=HTTP",
		LAST);

	web_submit_data("search",
		"Action=http://parity.test/app/search",
		"Method=POST",
		"Referer=http://parity.test/app/home",
		"Mode=HTTP",
		ITEMDATA,
		"Name=q", "Value=widgets", ENDITEM,
		LAST);

	web_custom_request("results",
		"URL=http://parity.test/api/results",
		"Method=GET",
		"Referer=http://parity.test/app/search?q=widgets",
		LAST);

	return 0;
}
