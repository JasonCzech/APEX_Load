Action()
{
	web_url("WebTours",
		"URL=http://parity.test/WebTours/",
		"Resource=0",
		"RecContentType=text/html",
		"Snapshot=t1.inf",
		"Mode=HTML",
		LAST);

	web_submit_form("login.pl",
		"Snapshot=t2.inf",
		ITEMDATA,
		"Name=username", "Value=jojo", ENDITEM,
		"Name=password", "Value=bean", ENDITEM,
		"Name=login.x", "Value=57", ENDITEM,
		"Name=login.y", "Value=10", ENDITEM,
		STARTHIDDENS,
		"Name=__VIEWSTATE", "Value=VS-RECORDED", ENDITEM,
		"Name=csrf", "Value=CSRF-RECORDED", ENDITEM,
		ENDHIDDENS,
		LAST);

	return 0;
}
