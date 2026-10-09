Action()
{
	web_reg_find("Text=X-Status: ok",
		"Search=Headers",
		LAST);

	web_reg_find("TextPfx=<title>",
		"TextSfx=</title>",
		LAST);

	web_reg_find("Text/DIG=Order ##",
		LAST);

	web_url("status",
		"URL=http://parity.test/app/status",
		"Resource=0",
		LAST);

	return 0;
}
