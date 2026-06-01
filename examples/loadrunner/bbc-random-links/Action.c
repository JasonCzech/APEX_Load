Action()
{
	lr_start_transaction("BBC_01_Home");
	web_url("BBC_Home",
		"URL=https://www.bbc.com/",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("BBC_01_Home", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("BBC_02_Random_Link_1");
	web_url("BBC_Random_Link_1",
		"URL={BBCLink01}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("BBC_02_Random_Link_1", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("BBC_03_Random_Link_2");
	web_url("BBC_Random_Link_2",
		"URL={BBCLink02}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("BBC_03_Random_Link_2", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("BBC_04_Random_Link_3");
	web_url("BBC_Random_Link_3",
		"URL={BBCLink03}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("BBC_04_Random_Link_3", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("BBC_05_Random_Link_4");
	web_url("BBC_Random_Link_4",
		"URL={BBCLink04}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("BBC_05_Random_Link_4", LR_AUTO);

	lr_think_time(1);

	lr_start_transaction("BBC_06_Random_Link_5");
	web_url("BBC_Random_Link_5",
		"URL={BBCLink05}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTML",
		LAST);
	lr_end_transaction("BBC_06_Random_Link_5", LR_AUTO);

	return 0;
}
