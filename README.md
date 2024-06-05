# Qase Robot Framework Reporter

Integrate Robot Framework and Qase using XML Report. This is an alternative of [Qase's official Robot Framework listener library](https://github.com/qase-tms/qase-python/tree/master/qase-robotframework).

The official library has some issues and limitations for my projects, so I decided to create my own CLI tool to parse Robot Framework's XML output and send the results to Qase. This tool is written in Go.

## Installation

```bash
go install github.com/petrabarus/qase-robotframework-reporter@latest
```

## Usage

To use this tool, you need to generate Robot Framework's XML output first, for example `output.xml`. Once you have the XML output, you can run the following command:

```bash
qase-robotframework-reporter \
    --api-token <Qase API token> \
    --project <Qase project code> \
    --run-title <Run title> \
    output.xml
```

You can also use the official environment variables to set the API token and project code:

```bash
QASE_TESTOPS_API_TOKEN=XXX \
    QASE_TESTOPS_PROJECT=XXX \
    QASE_TESTOPS_RUN_TITLE="Test Run $(date +%Y-%m-%d %H:%M:%S)" \
    qase-robotframework-reporter output.xml
```

## License

This project is licensed under the BSD 2-Clause - see the [LICENSE](LICENSE) file for details. Use it for free for personal or commercial purposes.