Action()
{
	web_add_cookie("consent=yes");
	web_add_cookie("sid=injected");

	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		LAST);

	web_url("account",
		"URL=http://parity.test/app/account",
		"Resource=0",
		LAST);

	web_remove_cookie("consent");

	web_url("final",
		"URL=http://parity.test/app/final",
		"Resource=0",
		LAST);

	return 0;
}
