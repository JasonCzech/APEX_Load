Action()
{
	web_reg_save_param("HeaderRef",
		"LB=X-Session-Ref: ",
		"RB=\r\n",
		"Search=Headers",
		LAST);

	web_reg_save_param("DefaultRef",
		"LB=Order-Ref: ",
		"RB=\r\n",
		LAST);

	web_reg_save_param_ex("ParamName=BodyTok",
		"LB=tok=",
		"RB=;",
		SEARCH_FILTERS,
		"Scope=Body",
		"IgnoreRedirections=Yes",
		"RequestUrl=*orders*",
		LAST);

	web_url("orders",
		"URL=http://parity.test/app/orders",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	web_url("confirm",
		"URL=http://parity.test/app/confirm?h={HeaderRef}&d={DefaultRef}&b={BodyTok}",
		"Resource=0",
		LAST);

	return 0;
}
