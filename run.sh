git checkout --orphan clean

git add -A

git commit -m "chore: init"

git branch -D main

git branch -m main

git push -f origin main
