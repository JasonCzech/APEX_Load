Action()
{
	web_reg_save_param_ex("ParamName=AuthCode",
		"LB=code=",
		"RB=&",
		SEARCH_FILTERS,
		"Scope=Headers",
		LAST);

	web_reg_save_param_ex("ParamName=ResourceTok",
		"LB=rt=",
		"RB=;",
		SEARCH_FILTERS,
		"RequestUrl=*token*",
		LAST);

	web_url("sso_start",
		"URL=http://parity.test/sso/start",
		"Resource=0",
		"RecContentType=text/html",
		EXTRARES,
		"Url=http://parity.test/static/token.js", ENDITEM,
		LAST);

	web_url("use",
		"URL=http://parity.test/app/use?code={AuthCode}&rt={ResourceTok}",
		"Resource=0",
		LAST);

	return 0;
}
