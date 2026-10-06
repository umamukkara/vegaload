# data_env.py -- feed a scenario rows from a file and a setting from the
# environment. Each iteration takes the next row of widgets.csv, so the
# virtual users create different widgets instead of the same one.
#
# API_KEY is read with -secret-env, so its value is taken out of the
# summary, the JSON output, the audit log and the console. (The sample app
# ignores the key. A real API would check it.)
#
#   cd ../sample-app && go run .                  # in one terminal
#   API_KEY=demo-key vegaload run -vus 5 -duration 10s \
#     -data data/widgets.csv -secret-env API_KEY data_env.py
#
# Flags go before the scenario file. The data file is called `widgets`
# because that is the file name without ".csv"; give another name with
# -data name=path.
import json

BASE = "http://127.0.0.1:8080"


def iteration():
    # data.widgets.next() takes the next row, in file order, shared by all
    # users. It starts again after the last row. data.widgets.random()
    # takes any row, and len(data.widgets) is the number of rows.
    row = data.widgets.next()

    res = http.post(
        BASE + "/widgets",
        body=json.dumps({"name": row["name"]}),
        headers={"X-Api-Key": env.API_KEY},
    )

    check(res, {
        "status is 201": lambda r: r.status == 201,
        "created " + row["name"]: lambda r: r.json()["name"] == row["name"],
    })
