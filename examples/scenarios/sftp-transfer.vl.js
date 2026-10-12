// sftp-transfer.vl.js -- upload a file, check it, and list a directory.
//
// You need an SFTP server on localhost:22 and an account that may write
// upload/. Put the password and the host key fingerprint in environment
// variables:
//   SFTP_PASSWORD=secret SFTP_HOST_KEY=SHA256:... vegaload run -vus 4 -duration 10s \
//     -env SFTP_HOST_KEY -secret-env SFTP_PASSWORD sftp-transfer.vl.js
//
// A call does not throw when the server fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
// Each user keeps one set of SSH connections for the whole run.
export default function () {
  const url = "sftp://localhost";
  const login = {
    username: "vegaload",
    password: env.SFTP_PASSWORD,
    host_key: env.SFTP_HOST_KEY,
    allow_writes: true,
  };

  // {id} is a new name on every call. roundtrip deletes that file.
  const rt = sftp.roundtrip(url, { ...login, path: "upload/vl-{id}.bin", size: "1MiB" });
  check(rt, { "file roundtrip": (r) => r.ok && r.size === 1048576 });

  // Change the path and expect_size to a file that is already on the server.
  const listed = sftp.list(url, {
    username: "vegaload",
    password: env.SFTP_PASSWORD,
    host_key: env.SFTP_HOST_KEY,
    path: "upload",
  });
  check(listed, { "upload listed": (r) => r.ok });
  const got = sftp.download(url, {
    username: "vegaload",
    password: env.SFTP_PASSWORD,
    host_key: env.SFTP_HOST_KEY,
    path: "upload/readme.txt",
    expect_size: "12",
  });
  check(got, { "readme downloaded": (r) => r.ok && r.size === 12 });
}
