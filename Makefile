BINARY_NAME=livematrix
OUTPUT_DIR=${CURDIR}/output

livematrix:
	echo "Building..."

build:
	rm -rf ${OUTPUT_DIR} && mkdir -p ${OUTPUT_DIR} && cp ${CURDIR}/.env.prod ${OUTPUT_DIR}/.env
	GOARCH=amd64 GOOS=linux go build -o ${OUTPUT_DIR}/${BINARY_NAME} main.go


