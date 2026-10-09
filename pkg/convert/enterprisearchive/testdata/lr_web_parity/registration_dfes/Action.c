Action()
{
	web_reg_save_param_ex("ParamName=Decoded",
		"LB=v=",
		"RB=;",
		"DFEs=Base64Decode",
		LAST);

	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		LAST);

	web_url("next",
		"URL=http://parity.test/app/next?d={Decoded}",
		"Resource=0",
		LAST);

	return 0;
}
