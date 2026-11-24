class Pager:
    def close(self):  # def Pager.close
        return None

    def start(self):  # def Pager.start
        return None


def shut(stdin, worker):
    stdin.close()  # call close -> none
    worker.start()  # call start -> none
