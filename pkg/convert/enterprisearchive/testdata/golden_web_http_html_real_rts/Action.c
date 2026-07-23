#include "web_api.h"

Action()
{
	web_reg_save_param("SessionID",
		"LB=sid=",
		"RB=;",
		LAST);

	lr_start_transaction("Golden Login");

	web_url("Homepage",
		"URL=https://shop.example.test/app/home",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	web_url("Homepage stylesheet",
		"URL=https://shop.example.test/static/app.css",
		"Resource=1",
		"RecContentType=text/css",
		LAST);

	web_url("Homepage logo",
		"URL=https://cdn.example.test/images/logo.png",
		"Resource=1",
		"RecContentType=image/png",
		LAST);

	web_reg_find("Text=Welcome",
		LAST);

	web_submit_data("Login_Submit",
		"Action=https://shop.example.test/app/login",
		"Method=POST",
		"RecContentType=text/html",
		"Referer=https://shop.example.test/app/home",
		ITEMDATA,
		"Name=username", "Value={UserName}", ENDITEM,
		"Name=password", "Value={UserPassword}", ENDITEM,
		LAST);

	lr_end_transaction("Golden Login", LR_AUTO);

	web_custom_request("Orders API",
		"URL=https://shop.example.test/api/orders",
		"Method=GET",
		"RecContentType=application/json",
		LAST);

	return 0;
}
