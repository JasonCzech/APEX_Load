Action()
{
	web_reg_save_param_ex("ParamName=CookieSid",
		"LB=sid=",
		"RB=;",
		SEARCH_FILTERS,
		"Scope=Cookies",
		LAST);

	web_reg_save_param_ex("ParamName=ResourceTok",
		"LB=rt=",
		"RB=;",
		SEARCH_FILTERS,
		"RequestUrl=*app.js*",
		LAST);

	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	return 0;
}
