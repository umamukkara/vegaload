// ftp-transfer.vl.js -- upload a file, check it, and list a directory.
//
// You need an FTP server on localhost:21 and an account that may write
// /upload. Put the password in an environment variable:
//   FTP_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env FTP_PASSWORD ftp-transfer.vl.js
//
// A call does not throw when the server fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
// Each user keeps one session pool for the whole run.
export default function () {
  const url = "ftp://localhost:21";
  const login = { username: "vegaload", password: env.FTP_PASSWORD, allow_writes: true };

  // {id} is a new name on every call. roundtrip deletes that file.
  const rt = ftp.roundtrip(url, { ...login, path: "/upload/vl-{id}.bin", size: "1MiB" });
  check(rt, { "file roundtrip": (r) => r.ok && r.size === 1048576 });

  // Change the path and expect_size to a file that is already on the server.
  const listed = ftp.list(url, { username: "vegaload", password: env.FTP_PASSWORD, path: "/upload" });
  check(listed, { "upload listed": (r) => r.ok });
  const got = ftp.download(url, {
    username: "vegaload",
    password: env.FTP_PASSWORD,
    path: "/upload/readme.txt",
    expect_size: "12",
  });
  check(got, { "readme downloaded": (r) => r.ok && r.size === 12 });
}
