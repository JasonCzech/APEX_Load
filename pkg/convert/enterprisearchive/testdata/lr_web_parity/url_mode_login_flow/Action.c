Action()
{
	web_reg_save_param_ex("ParamName=SessionID",
		"LB=name=\"session\" value=\"",
		"RB=\"",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTTP",
		LAST);

	lr_start_transaction("Login");

	web_submit_data("login",
		"Action=http://parity.test/app/login",
		"Method=POST",
		"RecContentType=text/html",
		"Mode=HTTP",
		ITEMDATA,
		"Name=user", "Value=jojo", ENDITEM,
		"Name=session", "Value={SessionID}", ENDITEM,
		LAST);

	lr_end_transaction("Login", LR_AUTO);

	lr_think_time(2);

	web_custom_request("orders",
		"URL=http://parity.test/api/orders?session={SessionID}",
		"Method=POST",
		"EncType=application/json",
		"Body={\"item\":\"widget\",\"qty\":2}",
		LAST);

	return 0;
}
