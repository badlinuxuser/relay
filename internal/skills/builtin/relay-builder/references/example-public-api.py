"""Example: a tool that uses a public web API with NO credentials at all: the weather (Open-Meteo).

Create it with create_mcp_server:  id "weather", kind "connector", no env, no oauth,
and a skill (required): when to use it ("Use when the user asks about the weather or the forecast"),
that the city name may be in any language, that the answer is in metric units.
"""
import os

from relay_mcp import Server, ToolError, http

srv = Server("weather")
GEO = os.environ.get("GEOCODE_API", "https://geocoding-api.open-meteo.com/v1/search")
FORECAST = os.environ.get("FORECAST_API", "https://api.open-meteo.com/v1/forecast")


def place(city):
    found = http("GET", GEO, params={"name": city, "count": 1, "language": "en"}).get("results")
    if not found:
        raise ToolError("no place called %r was found; try the name in English or add the country" % city)
    return found[0]


@srv.tool("current_weather", "The weather right now in a city: temperature, wind, and whether it rains.",
          {"city": "string: the name of a city, in any language, e.g. Berlin or Москва"})
def current_weather(city):
    p = place(city)
    data = http("GET", FORECAST, params={"latitude": p["latitude"], "longitude": p["longitude"],
                                          "current": "temperature_2m,wind_speed_10m,precipitation"})
    cur = data["current"]
    return "%s, %s: %s°C, wind %s km/h, precipitation %s mm" % (
        p["name"], p.get("country", ""), cur["temperature_2m"], cur["wind_speed_10m"], cur["precipitation"])


@srv.tool("forecast", "The forecast for the next days in a city: the highest and lowest temperature and the rain for each day.",
          {"city": "string", "days": "integer?: how many days (default 3, at most 7)"})
def forecast(city, days=3):
    p = place(city)
    d = http("GET", FORECAST, params={"latitude": p["latitude"], "longitude": p["longitude"], "timezone": "auto",
                                       "forecast_days": min(max(days, 1), 7),
                                       "daily": "temperature_2m_max,temperature_2m_min,precipitation_sum"})["daily"]
    return "\n".join("%s: %s…%s°C, rain %s mm" % (day, d["temperature_2m_min"][i], d["temperature_2m_max"][i], d["precipitation_sum"][i])
                     for i, day in enumerate(d["time"]))


srv.run()
