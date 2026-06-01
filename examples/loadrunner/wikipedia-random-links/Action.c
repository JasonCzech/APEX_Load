Action()
{
	lr_start_transaction("Wikipedia_01_Home");
	web_url("Wikipedia_Home",
		"URL=https://www.wikipedia.org/",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("Wikipedia_01_Home", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("Wikipedia_02_Random_Link_1");
	web_url("Wikipedia_Random_Link_1",
		"URL={WikiLink01}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("Wikipedia_02_Random_Link_1", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("Wikipedia_03_Random_Link_2");
	web_url("Wikipedia_Random_Link_2",
		"URL={WikiLink02}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("Wikipedia_03_Random_Link_2", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("Wikipedia_04_Random_Link_3");
	web_url("Wikipedia_Random_Link_3",
		"URL={WikiLink03}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("Wikipedia_04_Random_Link_3", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("Wikipedia_05_Random_Link_4");
	web_url("Wikipedia_Random_Link_4",
		"URL={WikiLink04}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("Wikipedia_05_Random_Link_4", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("Wikipedia_06_Random_Link_5");
	web_url("Wikipedia_Random_Link_5",
		"URL={WikiLink05}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("Wikipedia_06_Random_Link_5", LR_AUTO);

	return 0;
}
