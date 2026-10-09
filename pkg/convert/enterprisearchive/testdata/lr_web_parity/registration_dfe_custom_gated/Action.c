Action()
{
	web_reg_save_param_ex("ParamName=Custom",
		"LB=x=",
		"RB=;",
		"DFEs=MyCompanyDecoder",
		LAST);

	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		LAST);

	return 0;
}
