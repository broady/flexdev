# flexdev

[![Build Status](https://travis-ci.org/broady/flexdev.svg?branch=master)](https://travis-ci.org/broady/flexdev)

Command flexdev is a fast way to develop Go applications for App Engine flexible environment.

Get the tools:

    $ go get -u github.com/broady/flexdev
    $ go get -u google.golang.org/appengine/cmd/aedeploy

Ensure you are signed into [gcloud](https://cloud.google.com/sdk/):

    $ gcloud auth login

Deploy the flexdev server:

    $ flexdev server deploy -project your-project -version flexdev

Deploy your application code quickly.

    $ aedeploy flexdev deploy -target=https://flexdev-dot-your-project.appspot.com app.yaml

## What to expect

Depending on the number of files you're uploading, you should see sub-10 second code pushes:

    $ time aedeploy flexdev deploy -target=https://flexdev-dot-cbro-scratch.appspot.com app.yaml 
    2016/05/06 23:44:02 Remote message: Build created.
    2016/05/06 23:44:02 Build id: 1462603439818096410
    2016/05/06 23:44:02 All files sent.
    2016/05/06 23:44:06 Remote message: App is running.
    2016/05/06 23:44:06 Build successful. App is available at:

       https://flexdev-dot-cbro-scratch.appspot.com


    real 0m5.528s
    user 0m0.148s
    sys  0m0.167s

## Support

This is not an official Google product, just an experiment.

## License

See [LICENSE](LICENSE).
