Action()
{
	web_set_user("alice", "s3cret", "http://parity.test");

	web_url("secure",
		"URL=http://parity.test/secure/home",
		"Resource=0",
		LAST);

	web_url("elsewhere",
		"URL=http://alias.parity.test/public/asset",
		"Resource=0",
		LAST);

	return 0;
}
