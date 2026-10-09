Action()
{
	web_url("logo.png",
		"URL=http://parity.test/static/missing.png",
		"Resource=1",
		"RecContentType=image/png",
		"Mode=HTTP",
		LAST);

	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);

	return 0;
}
