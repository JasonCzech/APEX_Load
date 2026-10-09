Action()
{
	web_url("WebTours",
		"URL=http://parity.test/WebTours/",
		"Resource=0",
		"RecContentType=text/html",
		"Snapshot=t1.inf",
		"Mode=HTML",
		LAST);

	web_link("Flights",
		"Text=Flights",
		"Snapshot=t2.inf",
		LAST);

	web_image("Search Flights Button",
		"Src=/WebTours/images/flights.gif",
		"Snapshot=t3.inf",
		LAST);

	return 0;
}
