setup:
	npm install json-to-env -g

sort-playlists:
	cd functions/sort-playlists && json-to-env ./env.json ./env.sh
	cd functions/sort-playlists && source ./env.sh && rm ./env.sh && go run *.go

deploy-sort-playlists:
	apex build sort-playlists >/dev/null
	apex deploy sort-playlists --region eu-west-1 -ldebug --env-file ./functions/sort-playlists/env.json

to-spotify:
	cd functions/to-spotify && source ./env.sh && python3 main.py
setup-to-spotify:
	aws ecr create-repository --repository-name to-spotify --image-scanning-configuration scanOnPush=true --image-tag-mutability MUTABLE --region eu-west-1
deploy-to-spotify:
	aws ecr get-login-password --region eu-west-1 | docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com
	docker build -t to-spotify functions/to-spotify
	docker tag to-spotify:latest ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/to-spotify:latest
	docker push ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/to-spotify:latest
	sh ./utils/set-ecr.sh to-spotify ${AWS_ACCOUNT_ID}

to-www:
	cd functions/to-www && source ./env.sh && go run *.go
setup-to-www:
	aws ecr create-repository --repository-name to-www --image-scanning-configuration scanOnPush=true --image-tag-mutability MUTABLE --region eu-west-1
deploy-to-www:
	aws ecr get-login-password --region eu-west-1 | docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com
	docker build -t to-www functions/to-www
	docker tag to-www:latest ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/to-www:latest
	docker push ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/to-www:latest
	sh ./utils/set-ecr.sh to-www ${AWS_ACCOUNT_ID}

from-github:
	cd functions/from-github && source ./env.sh && go run *.go
setup-from-github:
	aws ecr create-repository --repository-name from-github --image-scanning-configuration scanOnPush=true --image-tag-mutability MUTABLE --region eu-west-1
deploy-from-github:
	aws ecr get-login-password --region eu-west-1 | docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com
	docker build -t from-github functions/from-github
	docker tag from-github:latest ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/from-github:latest
	docker push ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/from-github:latest
	sh ./utils/set-ecr.sh from-github ${AWS_ACCOUNT_ID}

from-discogs:
	cd functions/from-discogs && source ./env.sh && go run *.go
setup-from-discogs:
	aws ecr create-repository --repository-name from-discogs --image-scanning-configuration scanOnPush=true --image-tag-mutability MUTABLE --region eu-west-1
deploy-from-discogs:
	aws ecr get-login-password --region eu-west-1 | docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com
	docker build -t from-discogs functions/from-discogs
	docker tag from-discogs:latest ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/from-discogs:latest
	docker push ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/from-discogs:latest
	sh ./utils/set-ecr.sh from-discogs ${AWS_ACCOUNT_ID}

from-youtube:
	cd functions/from-youtube && source ./env.sh && go run *.go
setup-from-youtube:
	aws ecr create-repository --repository-name from-youtube --image-scanning-configuration scanOnPush=true --image-tag-mutability MUTABLE --region eu-west-1
deploy-from-youtube:
	aws ecr get-login-password --region eu-west-1 | docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com
	docker build -t from-youtube functions/from-youtube
	docker tag from-youtube:latest ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/from-youtube:latest
	docker push ${AWS_ACCOUNT_ID}.dkr.ecr.eu-west-1.amazonaws.com/from-youtube:latest
	sh ./utils/set-ecr.sh from-youtube ${AWS_ACCOUNT_ID}
